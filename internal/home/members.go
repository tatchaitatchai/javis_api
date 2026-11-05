package home

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"javis_api/internal/home/homedb"
	"javis_api/pkg/postgres"
)

const maxSeatsPerHome = 20

var (
	ErrMemberNotFound      = errors.New("member not found")
	ErrInvalidMemberUpdate = errors.New("invalid member update")
)

// Member deliberately has no phone number: joined members' numbers stay private.
type Member struct {
	UserID    uuid.UUID
	Name      *string
	Role      Role
	CanUnlock bool
}

// Roster is everyone in a home, joined or still invited.
type Roster struct {
	Members []Member
	Invites []Invite
}

// MemberChange holds the fields to change; nil leaves a field as it is.
type MemberChange struct {
	Role      *Role
	CanUnlock *bool
}

// inHome runs fn while holding the home's row lock, after checking the caller's
// role under that lock. Membership changes are serialized this way, so a person
// removed or demoted a moment earlier cannot still act on their old role.
func (s *Service) inHome(ctx context.Context, userID, homeID uuid.UUID, minimum Role, fn func(queries *homedb.Queries, role Role) error) error {
	return postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := homedb.New(tx)
		if err := queries.LockHome(ctx, homeID); err != nil {
			return err
		}
		role, err := queries.GetRole(ctx, homedb.GetRoleParams{HomeID: homeID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrHomeNotFound
		}
		if err != nil {
			return err
		}
		if !Role(role).atLeast(minimum) {
			return ErrForbidden
		}
		return fn(queries, Role(role))
	})
}

func (s *Service) ListMembers(ctx context.Context, userID, homeID uuid.UUID) (Roster, error) {
	if err := s.RequireRole(ctx, userID, homeID, RoleMember); err != nil {
		return Roster{}, err
	}
	memberRows, err := s.queries.ListMembers(ctx, homeID)
	if err != nil {
		return Roster{}, err
	}
	inviteRows, err := s.queries.ListOpenInvites(ctx, homeID)
	if err != nil {
		return Roster{}, err
	}
	roster := Roster{
		Members: make([]Member, 0, len(memberRows)),
		Invites: make([]Invite, 0, len(inviteRows)),
	}
	for _, row := range memberRows {
		roster.Members = append(roster.Members, Member{
			UserID: row.UserID, Name: row.Name, Role: Role(row.Role), CanUnlock: row.CanUnlock,
		})
	}
	for _, row := range inviteRows {
		roster.Invites = append(roster.Invites, Invite(row))
	}
	return roster, nil
}

// UpdateMember is owner-only because role and unlock right control who can
// open the door. The owner's own row cannot be changed.
func (s *Service) UpdateMember(ctx context.Context, ownerID, homeID, targetID uuid.UUID, change MemberChange) (Member, error) {
	var member Member
	err := s.inHome(ctx, ownerID, homeID, RoleOwner, func(queries *homedb.Queries, _ Role) error {
		if !change.valid() {
			return ErrInvalidMemberUpdate
		}
		target, err := queries.GetRole(ctx, homedb.GetRoleParams{HomeID: homeID, UserID: targetID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMemberNotFound
		}
		if err != nil {
			return err
		}
		if Role(target) == RoleOwner {
			return ErrForbidden
		}
		err = queries.UpdateMember(ctx, homedb.UpdateMemberParams{
			Role:      (*string)(change.Role),
			CanUnlock: change.CanUnlock,
			HomeID:    homeID,
			UserID:    targetID,
		})
		if err != nil {
			return err
		}
		row, err := queries.GetMember(ctx, homedb.GetMemberParams{HomeID: homeID, UserID: targetID})
		if err != nil {
			return err
		}
		member = Member{UserID: row.UserID, Name: row.Name, Role: Role(row.Role), CanUnlock: row.CanUnlock}
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	return member, nil
}

func (c MemberChange) valid() bool {
	if c.Role == nil && c.CanUnlock == nil {
		return false
	}
	return c.Role == nil || invitableRole(*c.Role)
}

// RemoveMember also covers leaving a home, which is removing yourself. The row
// is deleted, so access and the unlock right end with the next request.
func (s *Service) RemoveMember(ctx context.Context, actorID, homeID, targetID uuid.UUID) error {
	return s.inHome(ctx, actorID, homeID, RoleMember, func(queries *homedb.Queries, actor Role) error {
		self := actorID == targetID
		if actor == RoleMember && !self {
			return ErrForbidden
		}
		target, err := queries.GetRole(ctx, homedb.GetRoleParams{HomeID: homeID, UserID: targetID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMemberNotFound
		}
		if err != nil {
			return err
		}
		if !mayRemove(actor, Role(target), self) {
			return ErrForbidden
		}
		return queries.RemoveMember(ctx, homedb.RemoveMemberParams{HomeID: homeID, UserID: targetID})
	})
}

func mayRemove(actor, target Role, self bool) bool {
	switch {
	case target == RoleOwner:
		return false
	case self, actor == RoleOwner:
		return true
	default:
		return target == RoleMember
	}
}
