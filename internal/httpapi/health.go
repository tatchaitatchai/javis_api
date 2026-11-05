package httpapi

import (
	"context"
	"time"

	"javis_api/internal/httpapi/openapi"
)

// healthHandler implements the health operations of the contract.
type healthHandler struct {
	ping    func(context.Context) error
	timeout time.Duration
}

func (h healthHandler) GetLiveness(context.Context, openapi.GetLivenessRequestObject) (openapi.GetLivenessResponseObject, error) {
	return openapi.GetLiveness200JSONResponse{Status: openapi.LivenessStatusStatusOk}, nil
}

func (h healthHandler) GetReadiness(ctx context.Context, _ openapi.GetReadinessRequestObject) (openapi.GetReadinessResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	if err := h.ping(ctx); err != nil {
		// The ping error may contain connection details; it is never returned or logged.
		return openapi.GetReadiness503JSONResponse{Error: openapi.Error{
			Code:    "database_unavailable",
			Message: "Database is unavailable",
		}}, nil
	}
	return openapi.GetReadiness200JSONResponse{Status: openapi.ReadinessStatusStatusReady}, nil
}
