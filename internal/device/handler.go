package device

import (
	"context"
	"errors"

	"javis_api/internal/auth"
	"javis_api/internal/home"
	"javis_api/internal/httpapi/openapi"
)

// Handler implements the device operations of the contract.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func invalidDevice() openapi.ErrorResponse {
	return openapi.NewError("invalid_device", "Device details are not valid")
}

func deviceNotFound() openapi.ErrorResponse {
	return openapi.NewError("device_not_found", "This device is not in the home")
}

func (h *Handler) ListDevices(ctx context.Context, request openapi.ListDevicesRequestObject) (openapi.ListDevicesResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.ListDevices401JSONResponse(openapi.Unauthorized()), nil
	}
	devices, err := h.service.List(ctx, principal.UserID, request.HomeId)
	if errors.Is(err, home.ErrHomeNotFound) {
		return openapi.ListDevices404JSONResponse(openapi.HomeNotFound()), nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]openapi.Device, 0, len(devices))
	for _, device := range devices {
		items = append(items, toAPIDevice(device))
	}
	return openapi.ListDevices200JSONResponse{Items: items}, nil
}

func (h *Handler) ClaimDevice(ctx context.Context, request openapi.ClaimDeviceRequestObject) (openapi.ClaimDeviceResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.ClaimDevice401JSONResponse(openapi.Unauthorized()), nil
	}
	device, created, err := h.service.Claim(ctx, principal.UserID, request.HomeId, Claim{
		Provider:    request.Body.Provider,
		ExternalRef: request.Body.ExternalRef,
		Kind:        request.Body.Kind,
		Name:        request.Body.Name,
		ZoneID:      request.Body.ZoneId,
	})
	switch {
	case err == nil && created:
		return openapi.ClaimDevice201JSONResponse(toAPIDevice(device)), nil
	case err == nil:
		return openapi.ClaimDevice200JSONResponse(toAPIDevice(device)), nil
	case errors.Is(err, ErrInvalidDevice):
		return openapi.ClaimDevice400JSONResponse(invalidDevice()), nil
	case errors.Is(err, home.ErrForbidden):
		return openapi.ClaimDevice403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, home.ErrHomeNotFound):
		return openapi.ClaimDevice404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrDeviceClaimed):
		return openapi.ClaimDevice409JSONResponse(openapi.NewError("device_claimed", "This device belongs to another home")), nil
	case errors.Is(err, ErrDeviceLimit):
		return openapi.ClaimDevice409JSONResponse(openapi.NewError("device_limit_reached", "This home has the maximum number of devices")), nil
	}
	return nil, err
}

func (h *Handler) UpdateDevice(ctx context.Context, request openapi.UpdateDeviceRequestObject) (openapi.UpdateDeviceResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.UpdateDevice401JSONResponse(openapi.Unauthorized()), nil
	}
	change := Change{Name: request.Body.Name, ZoneID: request.Body.ZoneId}
	device, err := h.service.Update(ctx, principal.UserID, request.HomeId, request.DeviceId, change)
	switch {
	case err == nil:
		return openapi.UpdateDevice200JSONResponse(toAPIDevice(device)), nil
	case errors.Is(err, ErrInvalidDevice):
		return openapi.UpdateDevice400JSONResponse(invalidDevice()), nil
	case errors.Is(err, home.ErrForbidden):
		return openapi.UpdateDevice403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, home.ErrHomeNotFound):
		return openapi.UpdateDevice404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrDeviceMissing):
		return openapi.UpdateDevice404JSONResponse(deviceNotFound()), nil
	}
	return nil, err
}

func (h *Handler) RemoveDevice(ctx context.Context, request openapi.RemoveDeviceRequestObject) (openapi.RemoveDeviceResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.RemoveDevice401JSONResponse(openapi.Unauthorized()), nil
	}
	err := h.service.Remove(ctx, principal.UserID, request.HomeId, request.DeviceId)
	switch {
	case err == nil:
		return openapi.RemoveDevice204Response{}, nil
	case errors.Is(err, home.ErrForbidden):
		return openapi.RemoveDevice403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, home.ErrHomeNotFound):
		return openapi.RemoveDevice404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrDeviceMissing):
		return openapi.RemoveDevice404JSONResponse(deviceNotFound()), nil
	}
	return nil, err
}

func toAPIDevice(device Device) openapi.Device {
	return openapi.Device{
		Id:          device.ID,
		Provider:    openapi.DeviceProvider(device.Provider),
		ExternalRef: device.ExternalRef,
		Kind:        openapi.DeviceKind(device.Kind),
		Name:        device.Name,
		ZoneId:      device.ZoneID,
	}
}
