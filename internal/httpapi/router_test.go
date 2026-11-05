package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"javis_api/internal/auth"
)

func TestHealthContracts(t *testing.T) {
	tests := []struct {
		name, method, path string
		pingError          error
		status             int
		body               string
		wantPing           bool
	}{
		{"liveness ignores database", "GET", "/health/live", errors.New("offline"), 200, `{"status":"ok"}`, false},
		{"readiness connected", "GET", "/health/ready", nil, 200, `{"status":"ready"}`, true},
		{"readiness unavailable", "GET", "/health/ready", errors.New("postgres://user:secret@host/db"), 503, `{"error":{"code":"database_unavailable","message":"Database is unavailable"}}`, true},
		{"unknown endpoint", "GET", "/devices", nil, 404, `{"error":{"code":"not_found","message":"Endpoint not found"}}`, false},
		{"exact path", "GET", "/health/live/", nil, 404, `{"error":{"code":"not_found","message":"Endpoint not found"}}`, false},
		{"unsupported method", "POST", "/health/ready", nil, 405, `{"error":{"code":"method_not_allowed","message":"Method not allowed"}}`, false},
		{"unversioned product path", "GET", "/v1", nil, 404, `{"error":{"code":"not_found","message":"Endpoint not found"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pinged := false
			handler := newTestHandler(func(ctx context.Context) error {
				pinged = true
				if _, ok := ctx.Deadline(); !ok {
					t.Error("readiness ping has no deadline")
				}
				return tt.pingError
			}, time.Second)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
			if response.Code != tt.status || strings.TrimSpace(response.Body.String()) != tt.body {
				t.Fatalf("got HTTP %d %s", response.Code, response.Body)
			}
			if pinged != tt.wantPing {
				t.Errorf("database ping called = %v, want %v", pinged, tt.wantPing)
			}
			mediaType, _, _ := mime.ParseMediaType(response.Header().Get("Content-Type"))
			if mediaType != "application/json" || !json.Valid(response.Body.Bytes()) {
				t.Fatal("response must be JSON")
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-Id") == "" {
				t.Fatal("missing Cache-Control or X-Request-Id")
			}
			if tt.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != "GET" {
				t.Fatal("missing Allow header")
			}
		})
	}
}

func TestReadinessTimeoutAndRequestCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "request cancellation"}[canceled], func(t *testing.T) {
			request := httptest.NewRequest("GET", "/health/ready", nil)
			if canceled {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			var pingError error
			handler := newTestHandler(func(ctx context.Context) error {
				<-ctx.Done()
				pingError = ctx.Err()
				return pingError
			}, time.Millisecond)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			wantError := context.DeadlineExceeded
			if canceled {
				wantError = context.Canceled
			}
			if !errors.Is(pingError, wantError) || response.Code != http.StatusServiceUnavailable {
				t.Fatalf("ping error = %v, HTTP status = %d", pingError, response.Code)
			}
		})
	}
}

func newTestHandler(ping func(context.Context) error, timeout time.Duration) http.Handler {
	return NewHandler(Dependencies{
		Logger:        slog.New(slog.DiscardHandler),
		Ping:          ping,
		HealthTimeout: timeout,
		Auth:          auth.NewHandler(nil), // Health routes never reach the service.
	})
}
