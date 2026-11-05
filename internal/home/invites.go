package home

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"javis_api/internal/auth"
	"javis_api/internal/home/homedb"
	"javis_api/pkg/postgres"
)

const inviteValidDays = 7

var (
	ErrInvalidPhone       = errors.New("invalid phone")
	ErrInvalidRole        = errors.New("invalid role")
	ErrAlreadyMember      = errors.New("already a member")
	ErrAlreadyInvited     = errors.New("already invited")
	ErrMemberLimitReached = errors.New("member limit reached")
	ErrInviteNotFound     = errors.New("invite not found")
)

type Invite struct {
	ID        uuid.UUID
	Phone     string
	Role      string
	CanUnlock bool
	ExpiresAt time.Time
}

// MaskedPhone is the only form of a pending number that leaves the API.
func (i Invite) MaskedPhone() string {
	return "0" + i.Phone[3:5] + " xxx " + i.Phone[len(i.Phone)-4:]
}

// InviteRequest is what the inviter asks for.
type InviteRequest struct {
	Phone     string
	Role      Role
	CanUnlock bool
}

// ReceivedInvite is what the invitee sees before answering.
type ReceivedInvite struct {
	ID            uuid.UUID
	HomeName      string
	InvitedByName *string
	Role          string
	ZoneCount     int
	ExpiresAt     time.Time
}

func invitableRole(role Role) bool {
	return role == RoleAdmin || role == RoleMember
}

// CreateInvite lets an admin invite plain members only: the admin role and the
// unlock right are the owner's to give.
func (s *Service) CreateInvite(ctx context.Context, inviterID, homeID uuid.UUID, request InviteRequest) (Invite, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Invite{}, err
	}
	var invite Invite
	err = s.inHome(ctx, inviterID, homeID, RoleAdmin, func(queries *homedb.Queries, inviter Role) error {
		switch {
		case !auth.ValidPhone(request.Phone):
			return ErrInvalidPhone
		case !invitableRole(request.Role):
			return ErrInvalidRole
		case inviter != RoleOwner && (request.Role != RoleMember || request.CanUnlock):
			return ErrForbidden
		}
		member, err := queries.PhoneIsMember(ctx, homedb.PhoneIsMemberParams{HomeID: homeID, Phone: request.Phone})
		if err != nil {
			return err
		}
		if member {
			return ErrAlreadyMember
		}
		err = queries.ExpirePendingInvite(ctx, homedb.ExpirePendingInviteParams{HomeID: homeID, Phone: request.Phone})
		if err != nil {
			return err
		}
		row, err := queries.CreateInvite(ctx, homedb.CreateInviteParams{
			ID:        id,
			HomeID:    homeID,
			Phone:     request.Phone,
			Role:      string(request.Role),
			CanUnlock: request.CanUnlock,
			InvitedBy: inviterID,
			ValidDays: inviteValidDays,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAlreadyInvited
		}
		if err != nil {
			return err
		}
		seats, err := queries.CountSeats(ctx, homeID)
		if err != nil {
			return err
		}
		if seats > maxSeatsPerHome {
			return ErrMemberLimitReached
		}
		invite = Invite(row)
		return nil
	})
	if err != nil {
		return Invite{}, err
	}
	return invite, nil
}

func (s *Service) RevokeInvite(ctx context.Context, userID, homeID, inviteID uuid.UUID) error {
	return s.inHome(ctx, userID, homeID, RoleAdmin, func(queries *homedb.Queries, _ Role) error {
		revoked, err := queries.RevokeInvite(ctx, homedb.RevokeInviteParams{ID: inviteID, HomeID: homeID})
		if err != nil {
			return err
		}
		if revoked == 0 {
			return ErrInviteNotFound
		}
		return nil
	})
}

func (s *Service) ReceivedInvites(ctx context.Context, userID uuid.UUID) ([]ReceivedInvite, error) {
	phone, err := s.queries.GetUserPhone(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListReceivedInvites(ctx, phone)
	if err != nil {
		return nil, err
	}
	invites := make([]ReceivedInvite, 0, len(rows))
	for _, row := range rows {
		invites = append(invites, ReceivedInvite{
			ID:            row.ID,
			HomeName:      row.HomeName,
			InvitedByName: row.InvitedByName,
			Role:          row.Role,
			ZoneCount:     int(row.ZoneCount),
			ExpiresAt:     row.ExpiresAt,
		})
	}
	return invites, nil
}

// AcceptInvite proves identity by the phone number OTP verified: every lookup
// is scoped to it, so someone else's invitation looks like a missing one. The
// home is locked before the invitation, the same order CreateInvite takes.
func (s *Service) AcceptInvite(ctx context.Context, userID, inviteID uuid.UUID) (Home, error) {
	phone, err := s.queries.GetUserPhone(ctx, userID)
	if err != nil {
		return Home{}, err
	}
	homeID, err := s.queries.GetOpenInviteHome(ctx, homedb.GetOpenInviteHomeParams{ID: inviteID, Phone: phone})
	if errors.Is(err, pgx.ErrNoRows) {
		return Home{}, ErrInviteNotFound
	}
	if err != nil {
		return Home{}, err
	}
	err = postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := homedb.New(tx)
		if err := queries.LockHome(ctx, homeID); err != nil {
			return err
		}
		invite, err := queries.LockOpenInvite(ctx, homedb.LockOpenInviteParams{ID: inviteID, Phone: phone})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		if err != nil {
			return err
		}
		err = queries.AddMember(ctx, homedb.AddMemberParams{
			HomeID:    homeID,
			UserID:    userID,
			Role:      invite.Role,
			CanUnlock: invite.CanUnlock,
		})
		if err != nil {
			return err
		}
		return queries.MarkInviteAccepted(ctx, inviteID)
	})
	if err != nil {
		return Home{}, err
	}
	return s.Get(ctx, userID, homeID)
}

func (s *Service) DeclineInvite(ctx context.Context, userID, inviteID uuid.UUID) error {
	phone, err := s.queries.GetUserPhone(ctx, userID)
	if err != nil {
		return err
	}
	declined, err := s.queries.DeclineInvite(ctx, homedb.DeclineInviteParams{ID: inviteID, Phone: phone})
	if err != nil {
		return err
	}
	if declined == 0 {
		return ErrInviteNotFound
	}
	return nil
}
