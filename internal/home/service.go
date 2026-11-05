// Package home implements homes, zones and the access rules every home-scoped
// feature relies on.
package home

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"javis_api/internal/home/homedb"
	"javis_api/pkg/postgres"
)

const (
	maxHomesPerOwner = 20
	maxZonesPerHome  = 50
	maxNameLength    = 60
)

// Role is a person's standing in a home.
type Role string

const (
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

func (r Role) atLeast(minimum Role) bool {
	return roleRank(r) >= roleRank(minimum)
}

func roleRank(role Role) int {
	return slices.Index([]Role{RoleMember, RoleAdmin, RoleOwner}, role)
}

var (
	ErrHomeNotFound     = errors.New("home not found")
	ErrForbidden        = errors.New("forbidden")
	ErrInvalidHome      = errors.New("invalid home")
	ErrInvalidZone      = errors.New("invalid zone")
	ErrHomeLimitReached = errors.New("home limit reached")
	ErrZoneLimitReached = errors.New("zone limit reached")
)

// presetKeys are the zones the client translates; everything else is a typed name.
var presetKeys = []string{
	"Living room", "Bedroom", "Kitchen", "Front yard", "Office", "Bathroom", "Kids room",
}

// ZoneInput holds exactly one of PresetKey or Name.
type ZoneInput struct {
	PresetKey *string
	Name      *string
}

type Zone struct {
	ID        uuid.UUID
	PresetKey *string
	Name      *string
}

type Home struct {
	ID    uuid.UUID
	Name  string
	Role  Role
	Zones []Zone
}

type Summary struct {
	ID          uuid.UUID
	Name        string
	Role        Role
	ZoneCount   int
	MemberCount int
}

type Service struct {
	pool    *pgxpool.Pool
	queries *homedb.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, queries: homedb.New(pool)}
}

// RequireRole is the access check for anything inside a home. A non-member gets
// ErrHomeNotFound, so home ids cannot be probed.
func (s *Service) RequireRole(ctx context.Context, userID, homeID uuid.UUID, minimum Role) error {
	role, err := s.queries.GetRole(ctx, homedb.GetRoleParams{HomeID: homeID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrHomeNotFound
	}
	if err != nil {
		return err
	}
	if !Role(role).atLeast(minimum) {
		return ErrForbidden
	}
	return nil
}

func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Summary, error) {
	rows, err := s.queries.ListHomes(ctx, userID)
	if err != nil {
		return nil, err
	}
	homes := make([]Summary, 0, len(rows))
	for _, row := range rows {
		homes = append(homes, Summary{
			ID:          row.ID,
			Name:        row.Name,
			Role:        Role(row.Role),
			ZoneCount:   int(row.ZoneCount),
			MemberCount: int(row.MemberCount),
		})
	}
	return homes, nil
}

// Create adds a home, its zones and the caller as owner in one transaction.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, name string, zones []ZoneInput) (Home, error) {
	name = strings.TrimSpace(name)
	if !validName(name) || len(zones) > maxZonesPerHome {
		return Home{}, ErrInvalidHome
	}
	cleaned := make([]ZoneInput, 0, len(zones))
	for _, zone := range zones {
		clean, err := cleanZone(zone)
		if err != nil {
			return Home{}, ErrInvalidHome
		}
		cleaned = append(cleaned, clean)
	}
	homeID, err := uuid.NewV7()
	if err != nil {
		return Home{}, err
	}
	home := Home{ID: homeID, Name: name, Role: RoleOwner, Zones: make([]Zone, 0, len(cleaned))}
	err = postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := homedb.New(tx)
		if err := queries.LockOwnerHomes(ctx, userID.String()); err != nil {
			return err
		}
		owned, err := queries.CountOwnedHomes(ctx, userID)
		if err != nil {
			return err
		}
		if owned >= maxHomesPerOwner {
			return ErrHomeLimitReached
		}
		if err := queries.CreateHome(ctx, homedb.CreateHomeParams{ID: homeID, Name: name, CreatedBy: userID}); err != nil {
			return err
		}
		if err := queries.AddOwner(ctx, homedb.AddOwnerParams{HomeID: homeID, UserID: userID}); err != nil {
			return err
		}
		for position, zone := range cleaned {
			created, err := insertZone(ctx, queries, homeID, zone, position)
			if err != nil {
				return err
			}
			home.Zones = append(home.Zones, created)
		}
		return nil
	})
	if err != nil {
		return Home{}, err
	}
	return home, nil
}

func (s *Service) Get(ctx context.Context, userID, homeID uuid.UUID) (Home, error) {
	row, err := s.queries.GetHome(ctx, homedb.GetHomeParams{UserID: userID, HomeID: homeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Home{}, ErrHomeNotFound
	}
	if err != nil {
		return Home{}, err
	}
	rows, err := s.queries.ListZones(ctx, homeID)
	if err != nil {
		return Home{}, err
	}
	zones := make([]Zone, 0, len(rows))
	for _, zone := range rows {
		zones = append(zones, Zone(zone))
	}
	return Home{ID: row.ID, Name: row.Name, Role: Role(row.Role), Zones: zones}, nil
}

func (s *Service) Rename(ctx context.Context, userID, homeID uuid.UUID, name string) (Home, error) {
	if err := s.RequireRole(ctx, userID, homeID, RoleAdmin); err != nil {
		return Home{}, err
	}
	name = strings.TrimSpace(name)
	if !validName(name) {
		return Home{}, ErrInvalidHome
	}
	if err := s.queries.RenameHome(ctx, homedb.RenameHomeParams{ID: homeID, Name: name}); err != nil {
		return Home{}, err
	}
	return s.Get(ctx, userID, homeID)
}

// Delete removes a home; zones and memberships cascade with it.
func (s *Service) Delete(ctx context.Context, userID, homeID uuid.UUID) error {
	if err := s.RequireRole(ctx, userID, homeID, RoleOwner); err != nil {
		return err
	}
	return s.queries.DeleteHome(ctx, homeID)
}

func (s *Service) AddZone(ctx context.Context, userID, homeID uuid.UUID, input ZoneInput) (Zone, error) {
	if err := s.RequireRole(ctx, userID, homeID, RoleAdmin); err != nil {
		return Zone{}, err
	}
	clean, err := cleanZone(input)
	if err != nil {
		return Zone{}, err
	}
	var zone Zone
	err = postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := homedb.New(tx)
		if err := queries.LockHome(ctx, homeID); err != nil {
			return err
		}
		stats, err := queries.ZoneStats(ctx, homeID)
		if err != nil {
			return err
		}
		if stats.ZoneCount >= maxZonesPerHome {
			return ErrZoneLimitReached
		}
		zone, err = insertZone(ctx, queries, homeID, clean, int(stats.NextPosition))
		return err
	})
	if err != nil {
		return Zone{}, err
	}
	return zone, nil
}

func insertZone(ctx context.Context, queries *homedb.Queries, homeID uuid.UUID, zone ZoneInput, position int) (Zone, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Zone{}, err
	}
	created, err := queries.CreateZone(ctx, homedb.CreateZoneParams{
		ID:        id,
		HomeID:    homeID,
		PresetKey: zone.PresetKey,
		Name:      zone.Name,
		Position:  int32(position),
	})
	if err != nil {
		return Zone{}, err
	}
	return Zone(created), nil
}

// cleanZone trims a typed name and rejects anything but one valid value.
func cleanZone(zone ZoneInput) (ZoneInput, error) {
	switch {
	case (zone.PresetKey == nil) == (zone.Name == nil):
		return ZoneInput{}, ErrInvalidZone
	case zone.PresetKey != nil:
		if !slices.Contains(presetKeys, *zone.PresetKey) {
			return ZoneInput{}, ErrInvalidZone
		}
		return zone, nil
	}
	name := strings.TrimSpace(*zone.Name)
	if !validName(name) {
		return ZoneInput{}, ErrInvalidZone
	}
	return ZoneInput{Name: &name}, nil
}

func validName(name string) bool {
	length := utf8.RuneCountInString(name)
	if length == 0 || length > maxNameLength {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}
