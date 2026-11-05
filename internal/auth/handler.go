package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"javis_api/internal/httpapi/openapi"
	"javis_api/pkg/httpx"
)

// Handler implements the auth operations of the contract.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RequestOtp(ctx context.Context, request openapi.RequestOtpRequestObject) (openapi.RequestOtpResponseObject, error) {
	challenge, err := h.service.RequestCode(ctx, request.Body.Phone, httpx.ClientIPFrom(ctx))
	var limited RateLimitedError
	switch {
	case err == nil:
		return openapi.RequestOtp202JSONResponse{
			ResendAfterSeconds: int(challenge.ResendAfter.Seconds()),
			ExpiresInSeconds:   int(challenge.ExpiresIn.Seconds()),
			DebugCode:          challenge.DebugCode,
		}, nil
	case errors.Is(err, ErrInvalidPhone):
		return openapi.RequestOtp400JSONResponse(openapi.NewError("invalid_phone", "Mobile number is not valid")), nil
	case errors.As(err, &limited):
		return openapi.RequestOtp429JSONResponse{
			Body:    openapi.NewError("rate_limited", "Too many requests. Please try again later"),
			Headers: openapi.RequestOtp429ResponseHeaders{RetryAfter: int(limited.RetryAfter.Seconds())},
		}, nil
	case errors.Is(err, ErrSMSUnavailable):
		return openapi.RequestOtp503JSONResponse(openapi.NewError("sms_unavailable", "The code could not be sent")), nil
	}
	return nil, err
}

func (h *Handler) VerifyOtp(ctx context.Context, request openapi.VerifyOtpRequestObject) (openapi.VerifyOtpResponseObject, error) {
	session, err := h.service.VerifyCode(ctx, request.Body.Phone, request.Body.Code)
	switch {
	case err == nil:
		return openapi.VerifyOtp200JSONResponse{
			Token:     session.Token,
			ExpiresAt: session.ExpiresAt,
			User:      toAPIUser(session.User),
		}, nil
	case errors.Is(err, ErrInvalidRequest):
		return openapi.VerifyOtp400JSONResponse(openapi.NewError("invalid_request", "Request is invalid")), nil
	case errors.Is(err, ErrInvalidCode):
		return openapi.VerifyOtp401JSONResponse(openapi.NewError("invalid_code", "Code is incorrect or has expired")), nil
	case errors.Is(err, ErrTooManyAttempts):
		return openapi.VerifyOtp429JSONResponse(openapi.NewError("too_many_attempts", "Too many attempts. Please request a new code")), nil
	}
	return nil, err
}

func (h *Handler) Logout(ctx context.Context, _ openapi.LogoutRequestObject) (openapi.LogoutResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return openapi.Logout401JSONResponse(openapi.Unauthorized()), nil
	}
	if err := h.service.Logout(ctx, principal); err != nil {
		return nil, err
	}
	return openapi.Logout204Response{}, nil
}

func (h *Handler) GetMe(ctx context.Context, _ openapi.GetMeRequestObject) (openapi.GetMeResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return openapi.GetMe401JSONResponse(openapi.Unauthorized()), nil
	}
	user, err := h.service.Me(ctx, principal)
	if errors.Is(err, ErrUnauthorized) {
		return openapi.GetMe401JSONResponse(openapi.Unauthorized()), nil
	}
	if err != nil {
		return nil, err
	}
	return openapi.GetMe200JSONResponse(toAPIUser(user)), nil
}

func (h *Handler) UpdateMe(ctx context.Context, request openapi.UpdateMeRequestObject) (openapi.UpdateMeResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return openapi.UpdateMe401JSONResponse(openapi.Unauthorized()), nil
	}
	user, err := h.service.SetName(ctx, principal, request.Body.Name)
	switch {
	case err == nil:
		return openapi.UpdateMe200JSONResponse(toAPIUser(user)), nil
	case errors.Is(err, ErrInvalidName):
		return openapi.UpdateMe400JSONResponse(openapi.NewError("invalid_name", "Name is not valid")), nil
	case errors.Is(err, ErrUnauthorized):
		return openapi.UpdateMe401JSONResponse(openapi.Unauthorized()), nil
	}
	return nil, err
}

func toAPIUser(user User) openapi.User {
	return openapi.User{Id: user.ID, Phone: user.Phone, Name: user.Name}
}

type principalKey struct{}

// PrincipalFrom returns the caller set by RequireSession.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

// RequireSession rejects requests without a valid bearer token. When the check
// itself fails it answers 503, so an outage never looks like a sign-out to the app.
func (h *Handler) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		principal, err := h.service.Authenticate(r.Context(), token)
		if errors.Is(err, ErrUnauthorized) {
			writeUnauthorized(w)
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "Service is unavailable")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpx.WriteJSON(w, http.StatusUnauthorized, openapi.Unauthorized())
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}
