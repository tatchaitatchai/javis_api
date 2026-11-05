// Package device records which home a device belongs to and what people call
// it. Live state and commands stay with the device provider.
package device

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"javis_api/internal/device/devicedb"
	"javis_api/internal/home"
	"javis_api/pkg/postgres"
)

const (
	maxDevicesPerHome = 100
	maxRefLength      = 128
	providerTuya      = "tuya"
	kindLight         = "light"
)

var (
	ErrInvalidDevice = errors.New("invalid device")
	ErrDeviceClaimed = errors.New("device belongs to another home")
	ErrDeviceLimit   = errors.New("device limit reached")
	ErrDeviceMissing = errors.New("device not found")
)

// Access is the part of the home module this one needs.
type Access interface {
	RequireRole(ctx context.Context, userID, homeID uuid.UUID, minimum home.Role) error
}

// Device never carries live state: that belongs to the provider.
type Device struct {
	ID          uuid.UUID
	Provider    string
	ExternalRef string
	Kind        string
	Name        *string
	ZoneID      *uuid.UUID
}

// Claim asks to register a paired device to a home.
type Claim struct {
	Provider    string
	ExternalRef string
	Kind        string
	Name        *string
	ZoneID      *uuid.UUID
}

// Change holds the fields to change; nil leaves a field as it is.
type Change struct {
	Name   *string
	ZoneID *uuid.UUID
}

type Service struct {
	pool    *pgxpool.Pool
	queries *devicedb.Queries
	access  Access
}

func NewService(pool *pgxpool.Pool, access Access) *Service {
	return &Service{pool: pool, queries: devicedb.New(pool), access: access}
}

func (s *Service) List(ctx context.Context, userID, homeID uuid.UUID) ([]Device, error) {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleMember); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListDevices(ctx, homeID)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(rows))
	for _, row := range rows {
		devices = append(devices, Device(row))
	}
	return devices, nil
}

// Claim is idempotent inside one home, because pairing can be retried. The
// returned bool is true when the device was newly registered.
func (s *Service) Claim(ctx context.Context, userID, homeID uuid.UUID, claim Claim) (Device, bool, error) {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleAdmin); err != nil {
		return Device{}, false, err
	}
	name, err := cleanName(claim.Name)
	if err != nil {
		return Device{}, false, err
	}
	if !validClaim(claim) {
		return Device{}, false, ErrInvalidDevice
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Device{}, false, err
	}
	var device Device
	created := false
	err = postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := devicedb.New(tx)
		if err := queries.LockHome(ctx, homeID); err != nil {
			return err
		}
		if err := requireZone(ctx, queries, homeID, claim.ZoneID); err != nil {
			return err
		}
		existing, err := queries.GetClaim(ctx, devicedb.GetClaimParams{Provider: claim.Provider, ExternalRef: claim.ExternalRef})
		switch {
		case err == nil && existing.HomeID != homeID:
			return ErrDeviceClaimed
		case err == nil:
			device = Device{
				ID: existing.ID, Provider: existing.Provider, ExternalRef: existing.ExternalRef,
				Kind: existing.Kind, Name: existing.Name, ZoneID: existing.ZoneID,
			}
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		count, err := queries.CountDevices(ctx, homeID)
		if err != nil {
			return err
		}
		if count >= maxDevicesPerHome {
			return ErrDeviceLimit
		}
		row, err := queries.ClaimDevice(ctx, devicedb.ClaimDeviceParams{
			ID:          id,
			HomeID:      homeID,
			ZoneID:      claim.ZoneID,
			Provider:    claim.Provider,
			ExternalRef: claim.ExternalRef,
			Kind:        claim.Kind,
			Name:        name,
			CreatedBy:   userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceClaimed // Another home claimed it a moment ago.
		}
		if err != nil {
			return err
		}
		device, created = Device(row), true
		return nil
	})
	if err != nil {
		return Device{}, false, err
	}
	return device, created, nil
}

func (s *Service) Update(ctx context.Context, userID, homeID, deviceID uuid.UUID, change Change) (Device, error) {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleAdmin); err != nil {
		return Device{}, err
	}
	if change.Name == nil && change.ZoneID == nil {
		return Device{}, ErrInvalidDevice
	}
	name, err := cleanName(change.Name)
	if err != nil {
		return Device{}, err
	}
	if err := requireZone(ctx, s.queries, homeID, change.ZoneID); err != nil {
		return Device{}, err
	}
	row, err := s.queries.UpdateDevice(ctx, devicedb.UpdateDeviceParams{
		Name: name, ZoneID: change.ZoneID, ID: deviceID, HomeID: homeID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrDeviceMissing
	}
	if err != nil {
		return Device{}, err
	}
	return Device(row), nil
}

func (s *Service) Remove(ctx context.Context, userID, homeID, deviceID uuid.UUID) error {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleAdmin); err != nil {
		return err
	}
	removed, err := s.queries.DeleteDevice(ctx, devicedb.DeleteDeviceParams{ID: deviceID, HomeID: homeID})
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrDeviceMissing
	}
	return nil
}

func validClaim(claim Claim) bool {
	length := utf8.RuneCountInString(claim.ExternalRef)
	return claim.Provider == providerTuya && claim.Kind == kindLight &&
		length > 0 && length <= maxRefLength &&
		!strings.ContainsFunc(claim.ExternalRef, unicode.IsControl)
}

func cleanName(name *string) (*string, error) {
	if name == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*name)
	if !home.ValidName(trimmed) {
		return nil, ErrInvalidDevice
	}
	return &trimmed, nil
}

func requireZone(ctx context.Context, queries *devicedb.Queries, homeID uuid.UUID, zoneID *uuid.UUID) error {
	if zoneID == nil {
		return nil
	}
	found, err := queries.ZoneInHome(ctx, devicedb.ZoneInHomeParams{ID: *zoneID, HomeID: homeID})
	if err != nil {
		return err
	}
	if !found {
		return ErrInvalidDevice
	}
	return nil
}
