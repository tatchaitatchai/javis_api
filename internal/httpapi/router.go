// Package httpapi assembles the HTTP surface: middleware, the generated
// OpenAPI routes and the module handlers that implement them.
package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"javis_api/internal/auth"
	"javis_api/internal/home"
	"javis_api/internal/httpapi/openapi"
	"javis_api/pkg/httpx"
)

// Dependencies are the concrete services the HTTP layer needs. cmd/api builds them.
type Dependencies struct {
	Logger *slog.Logger
	// Ping checks database connectivity and must honor its context.
	Ping          func(context.Context) error
	HealthTimeout time.Duration
	Auth          *auth.Handler
	Home          *home.Handler
}

// server implements openapi.StrictServerInterface by embedding one handler per
// module. Each feature module adds its handler here when its routes exist.
type server struct {
	healthHandler
	*authOperations
	*homeOperations
}

// Aliases give each module's embedded Handler a distinct field name.
type (
	authOperations = auth.Handler
	homeOperations = home.Handler
)

var _ openapi.StrictServerInterface = server{}

// maxRequestBody bounds JSON requests; no operation needs more.
const maxRequestBody = 64 << 10

// publicRoutes are the only operations reachable without a session. Every other
// route requires one, so a new operation is protected by default.
var publicRoutes = map[string]bool{
	"GET /health/live":          true,
	"GET /health/ready":         true,
	"POST /v1/auth/otp/request": true,
	"POST /v1/auth/otp/verify":  true,
}

// NewHandler returns the root handler. Routes come only from api/openapi.yaml.
func NewHandler(deps Dependencies) http.Handler {
	router := chi.NewRouter()
	router.Use(
		httpx.RequestID,
		httpx.AccessLog(deps.Logger, routePattern),
		httpx.Recover(deps.Logger),
		httpx.NoStore,
		httpx.ClientIP,
		httpx.LimitBody(maxRequestBody),
	)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Endpoint not found")
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", strings.Join(allowedMethods(router, r.URL.Path), ", "))
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	})

	strict := openapi.NewStrictHandlerWithOptions(server{
		healthHandler:  healthHandler{ping: deps.Ping, timeout: deps.HealthTimeout},
		authOperations: deps.Auth,
		homeOperations: deps.Home,
	}, nil, openapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: writeInvalidRequest,
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			// Handlers must map expected failures to contract responses; anything
			// reaching here is a bug. Log only the type: raw errors may hold secrets.
			deps.Logger.ErrorContext(r.Context(), "unhandled handler error",
				"request_id", httpx.RequestIDFrom(r.Context()),
				"error_type", fmt.Sprintf("%T", err))
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
		},
	})
	return openapi.HandlerWithOptions(strict, openapi.ChiServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: writeInvalidRequest,
		Middlewares:      []openapi.MiddlewareFunc{requireSessionUnlessPublic(deps.Auth)},
	})
}

func requireSessionUnlessPublic(authHandler *auth.Handler) openapi.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		protected := authHandler.RequireSession(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicRoutes[r.Method+" "+routePattern(r)] {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	}
}

func writeInvalidRequest(w http.ResponseWriter, _ *http.Request, _ error) {
	// Parameter and body decoding errors can echo client input; keep them out.
	httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Request is invalid")
}

// routePattern labels access logs with the matched route template so
// identifiers in paths are never logged.
func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePattern() != "" {
		return rctx.RoutePattern()
	}
	return "unmatched"
}

// allowedMethods rebuilds the Allow header, which chi does not pass to a
// custom MethodNotAllowed handler.
func allowedMethods(routes chi.Routes, path string) []string {
	var allowed []string
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	} {
		if routes.Match(chi.NewRouteContext(), method, path) {
			allowed = append(allowed, method)
		}
	}
	return allowed
}
