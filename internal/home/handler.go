package home

import (
	"context"
	"errors"

	"javis_api/internal/auth"
	"javis_api/internal/httpapi/openapi"
)

// Handler implements the home operations of the contract.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) ListHomes(ctx context.Context, _ openapi.ListHomesRequestObject) (openapi.ListHomesResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.ListHomes401JSONResponse(openapi.Unauthorized()), nil
	}
	homes, err := h.service.List(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	items := make([]openapi.HomeSummary, 0, len(homes))
	for _, home := range homes {
		items = append(items, openapi.HomeSummary{
			Id:          home.ID,
			Name:        home.Name,
			Role:        openapi.HomeRole(home.Role),
			ZoneCount:   home.ZoneCount,
			MemberCount: home.MemberCount,
		})
	}
	return openapi.ListHomes200JSONResponse{Items: items}, nil
}

func (h *Handler) CreateHome(ctx context.Context, request openapi.CreateHomeRequestObject) (openapi.CreateHomeResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.CreateHome401JSONResponse(openapi.Unauthorized()), nil
	}
	zones := make([]ZoneInput, 0, len(request.Body.Zones))
	for _, zone := range request.Body.Zones {
		zones = append(zones, ZoneInput{PresetKey: zone.PresetKey, Name: zone.Name})
	}
	home, err := h.service.Create(ctx, principal.UserID, request.Body.Name, zones)
	switch {
	case err == nil:
		return openapi.CreateHome201JSONResponse(toAPIHome(home)), nil
	case errors.Is(err, ErrInvalidHome):
		return openapi.CreateHome400JSONResponse(openapi.NewError("invalid_home", "Home name or zones are not valid")), nil
	case errors.Is(err, ErrHomeLimitReached):
		return openapi.CreateHome409JSONResponse(openapi.NewError("home_limit_reached", "You have reached the maximum number of homes")), nil
	}
	return nil, err
}

func (h *Handler) GetHome(ctx context.Context, request openapi.GetHomeRequestObject) (openapi.GetHomeResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.GetHome401JSONResponse(openapi.Unauthorized()), nil
	}
	home, err := h.service.Get(ctx, principal.UserID, request.HomeId)
	if errors.Is(err, ErrHomeNotFound) {
		return openapi.GetHome404JSONResponse(openapi.HomeNotFound()), nil
	}
	if err != nil {
		return nil, err
	}
	return openapi.GetHome200JSONResponse(toAPIHome(home)), nil
}

func (h *Handler) UpdateHome(ctx context.Context, request openapi.UpdateHomeRequestObject) (openapi.UpdateHomeResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.UpdateHome401JSONResponse(openapi.Unauthorized()), nil
	}
	home, err := h.service.Rename(ctx, principal.UserID, request.HomeId, request.Body.Name)
	switch {
	case err == nil:
		return openapi.UpdateHome200JSONResponse(toAPIHome(home)), nil
	case errors.Is(err, ErrInvalidHome):
		return openapi.UpdateHome400JSONResponse(openapi.NewError("invalid_home", "Home name is not valid")), nil
	case errors.Is(err, ErrForbidden):
		return openapi.UpdateHome403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, ErrHomeNotFound):
		return openapi.UpdateHome404JSONResponse(openapi.HomeNotFound()), nil
	}
	return nil, err
}

func (h *Handler) DeleteHome(ctx context.Context, request openapi.DeleteHomeRequestObject) (openapi.DeleteHomeResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.DeleteHome401JSONResponse(openapi.Unauthorized()), nil
	}
	err := h.service.Delete(ctx, principal.UserID, request.HomeId)
	switch {
	case err == nil:
		return openapi.DeleteHome204Response{}, nil
	case errors.Is(err, ErrForbidden):
		return openapi.DeleteHome403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, ErrHomeHasDevices):
		return openapi.DeleteHome409JSONResponse(openapi.NewError("home_has_devices", "Remove this home's devices first")), nil
	case errors.Is(err, ErrHomeNotFound):
		return openapi.DeleteHome404JSONResponse(openapi.HomeNotFound()), nil
	}
	return nil, err
}

func (h *Handler) AddZone(ctx context.Context, request openapi.AddZoneRequestObject) (openapi.AddZoneResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.AddZone401JSONResponse(openapi.Unauthorized()), nil
	}
	input := ZoneInput{PresetKey: request.Body.PresetKey, Name: request.Body.Name}
	zone, err := h.service.AddZone(ctx, principal.UserID, request.HomeId, input)
	switch {
	case err == nil:
		return openapi.AddZone201JSONResponse(toAPIZone(zone)), nil
	case errors.Is(err, ErrInvalidZone):
		return openapi.AddZone400JSONResponse(openapi.NewError("invalid_zone", "Zone is not valid")), nil
	case errors.Is(err, ErrForbidden):
		return openapi.AddZone403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, ErrHomeNotFound):
		return openapi.AddZone404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrZoneLimitReached):
		return openapi.AddZone409JSONResponse(openapi.NewError("zone_limit_reached", "This home has the maximum number of zones")), nil
	}
	return nil, err
}

func toAPIHome(home Home) openapi.Home {
	zones := make([]openapi.Zone, 0, len(home.Zones))
	for _, zone := range home.Zones {
		zones = append(zones, toAPIZone(zone))
	}
	return openapi.Home{Id: home.ID, Name: home.Name, Role: openapi.HomeRole(home.Role), CanUnlock: home.CanUnlock, Zones: zones}
}

func toAPIZone(zone Zone) openapi.Zone {
	return openapi.Zone{Id: zone.ID, PresetKey: zone.PresetKey, Name: zone.Name}
}
