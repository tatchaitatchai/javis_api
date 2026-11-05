package home

import (
	"context"
	"errors"

	"javis_api/internal/auth"
	"javis_api/internal/httpapi/openapi"
)

func inviteNotFound() openapi.ErrorResponse {
	return openapi.NewError("invite_not_found", "This invitation is no longer available")
}

func memberNotFound() openapi.ErrorResponse {
	return openapi.NewError("member_not_found", "This person is not in the home")
}

func (h *Handler) ListMembers(ctx context.Context, request openapi.ListMembersRequestObject) (openapi.ListMembersResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.ListMembers401JSONResponse(openapi.Unauthorized()), nil
	}
	roster, err := h.service.ListMembers(ctx, principal.UserID, request.HomeId)
	if errors.Is(err, ErrHomeNotFound) {
		return openapi.ListMembers404JSONResponse(openapi.HomeNotFound()), nil
	}
	if err != nil {
		return nil, err
	}
	response := openapi.ListMembers200JSONResponse{
		Members: make([]openapi.Member, 0, len(roster.Members)),
		Invites: make([]openapi.Invite, 0, len(roster.Invites)),
	}
	for _, member := range roster.Members {
		response.Members = append(response.Members, toAPIMember(member))
	}
	for _, invite := range roster.Invites {
		response.Invites = append(response.Invites, toAPIInvite(invite))
	}
	return response, nil
}

func (h *Handler) UpdateMember(ctx context.Context, request openapi.UpdateMemberRequestObject) (openapi.UpdateMemberResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.UpdateMember401JSONResponse(openapi.Unauthorized()), nil
	}
	change := MemberChange{CanUnlock: request.Body.CanUnlock}
	if request.Body.Role != nil {
		role := Role(*request.Body.Role)
		change.Role = &role
	}
	member, err := h.service.UpdateMember(ctx, principal.UserID, request.HomeId, request.UserId, change)
	switch {
	case err == nil:
		return openapi.UpdateMember200JSONResponse(toAPIMember(member)), nil
	case errors.Is(err, ErrInvalidMemberUpdate):
		return openapi.UpdateMember400JSONResponse(openapi.NewError("invalid_member_update", "Send a valid role or unlock right")), nil
	case errors.Is(err, ErrForbidden):
		return openapi.UpdateMember403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, ErrHomeNotFound):
		return openapi.UpdateMember404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrMemberNotFound):
		return openapi.UpdateMember404JSONResponse(memberNotFound()), nil
	}
	return nil, err
}

func (h *Handler) RemoveMember(ctx context.Context, request openapi.RemoveMemberRequestObject) (openapi.RemoveMemberResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.RemoveMember401JSONResponse(openapi.Unauthorized()), nil
	}
	err := h.service.RemoveMember(ctx, principal.UserID, request.HomeId, request.UserId)
	switch {
	case err == nil:
		return openapi.RemoveMember204Response{}, nil
	case errors.Is(err, ErrForbidden):
		return openapi.RemoveMember403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, ErrHomeNotFound):
		return openapi.RemoveMember404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrMemberNotFound):
		return openapi.RemoveMember404JSONResponse(memberNotFound()), nil
	}
	return nil, err
}

func (h *Handler) CreateInvite(ctx context.Context, request openapi.CreateInviteRequestObject) (openapi.CreateInviteResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.CreateInvite401JSONResponse(openapi.Unauthorized()), nil
	}
	invite, err := h.service.CreateInvite(ctx, principal.UserID, request.HomeId, InviteRequest{
		Phone:     request.Body.Phone,
		Role:      Role(request.Body.Role),
		CanUnlock: request.Body.CanUnlock,
	})
	switch {
	case err == nil:
		return openapi.CreateInvite201JSONResponse(toAPIInvite(invite)), nil
	case errors.Is(err, ErrInvalidPhone):
		return openapi.CreateInvite400JSONResponse(openapi.NewError("invalid_phone", "Enter a valid Thai mobile number")), nil
	case errors.Is(err, ErrInvalidRole):
		return openapi.CreateInvite400JSONResponse(openapi.NewError("invalid_role", "Role must be admin or member")), nil
	case errors.Is(err, ErrForbidden):
		return openapi.CreateInvite403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, ErrHomeNotFound):
		return openapi.CreateInvite404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrAlreadyMember):
		return openapi.CreateInvite409JSONResponse(openapi.NewError("already_member", "This number is already in the home")), nil
	case errors.Is(err, ErrAlreadyInvited):
		return openapi.CreateInvite409JSONResponse(openapi.NewError("already_invited", "This number already has an open invitation")), nil
	case errors.Is(err, ErrMemberLimitReached):
		return openapi.CreateInvite409JSONResponse(openapi.NewError("member_limit_reached", "This home has the maximum number of people")), nil
	}
	return nil, err
}

func (h *Handler) RevokeInvite(ctx context.Context, request openapi.RevokeInviteRequestObject) (openapi.RevokeInviteResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.RevokeInvite401JSONResponse(openapi.Unauthorized()), nil
	}
	err := h.service.RevokeInvite(ctx, principal.UserID, request.HomeId, request.InviteId)
	switch {
	case err == nil:
		return openapi.RevokeInvite204Response{}, nil
	case errors.Is(err, ErrForbidden):
		return openapi.RevokeInvite403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, ErrHomeNotFound):
		return openapi.RevokeInvite404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrInviteNotFound):
		return openapi.RevokeInvite404JSONResponse(inviteNotFound()), nil
	}
	return nil, err
}

func (h *Handler) ListReceivedInvites(ctx context.Context, _ openapi.ListReceivedInvitesRequestObject) (openapi.ListReceivedInvitesResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.ListReceivedInvites401JSONResponse(openapi.Unauthorized()), nil
	}
	invites, err := h.service.ReceivedInvites(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	items := make([]openapi.ReceivedInvite, 0, len(invites))
	for _, invite := range invites {
		items = append(items, openapi.ReceivedInvite{
			Id:            invite.ID,
			HomeName:      invite.HomeName,
			InvitedByName: invite.InvitedByName,
			Role:          openapi.InviteRole(invite.Role),
			ZoneCount:     invite.ZoneCount,
			ExpiresAt:     invite.ExpiresAt,
		})
	}
	return openapi.ListReceivedInvites200JSONResponse{Items: items}, nil
}

func (h *Handler) AcceptInvite(ctx context.Context, request openapi.AcceptInviteRequestObject) (openapi.AcceptInviteResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.AcceptInvite401JSONResponse(openapi.Unauthorized()), nil
	}
	home, err := h.service.AcceptInvite(ctx, principal.UserID, request.InviteId)
	if errors.Is(err, ErrInviteNotFound) {
		return openapi.AcceptInvite404JSONResponse(inviteNotFound()), nil
	}
	if err != nil {
		return nil, err
	}
	return openapi.AcceptInvite200JSONResponse(toAPIHome(home)), nil
}

func (h *Handler) DeclineInvite(ctx context.Context, request openapi.DeclineInviteRequestObject) (openapi.DeclineInviteResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.DeclineInvite401JSONResponse(openapi.Unauthorized()), nil
	}
	err := h.service.DeclineInvite(ctx, principal.UserID, request.InviteId)
	if errors.Is(err, ErrInviteNotFound) {
		return openapi.DeclineInvite404JSONResponse(inviteNotFound()), nil
	}
	if err != nil {
		return nil, err
	}
	return openapi.DeclineInvite204Response{}, nil
}

func toAPIMember(member Member) openapi.Member {
	return openapi.Member{
		UserId:    member.UserID,
		Name:      member.Name,
		Role:      openapi.HomeRole(member.Role),
		CanUnlock: member.CanUnlock,
	}
}

func toAPIInvite(invite Invite) openapi.Invite {
	return openapi.Invite{
		Id:          invite.ID,
		PhoneMasked: invite.MaskedPhone(),
		Role:        openapi.InviteRole(invite.Role),
		CanUnlock:   invite.CanUnlock,
		ExpiresAt:   invite.ExpiresAt,
	}
}
