package httpx

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteError(t *testing.T) {
	response := httptest.NewRecorder()
	WriteError(response, http.StatusNotFound, "not_found", "Endpoint not found")
	if response.Code != http.StatusNotFound || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got HTTP %d, Content-Type %q", response.Code, response.Header().Get("Content-Type"))
	}
	want := `{"error":{"code":"not_found","message":"Endpoint not found"}}`
	if got := strings.TrimSpace(response.Body.String()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name, inbound string
		keep          bool
	}{
		{"generates when missing", "", false},
		{"keeps well-formed inbound id", "edge-01.abc_DEF", true},
		{"replaces id with unsafe characters", "abc\ninjected", false},
		{"replaces overly long id", strings.Repeat("a", 65), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = RequestIDFrom(r.Context())
			}))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.inbound != "" {
				request.Header.Set(RequestIDHeader, tt.inbound)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if seen == "" || response.Header().Get(RequestIDHeader) != seen {
				t.Fatalf("context id %q, header id %q", seen, response.Header().Get(RequestIDHeader))
			}
			if (seen == tt.inbound) != tt.keep {
				t.Fatalf("inbound id kept = %v, want %v", seen == tt.inbound, tt.keep)
			}
			if !tt.keep && len(seen) != 32 {
				t.Fatalf("generated id %q is not 32 hex characters", seen)
			}
		})
	}
}

func TestRecoverWritesEnvelopeWithoutPanicValue(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := RequestID(Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("postgres://user:secret@host/db")
	})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"internal_error"`) {
		t.Fatalf("got HTTP %d %s", response.Code, response.Body)
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(response.Body.String(), "secret") {
		t.Fatal("panic value leaked into logs or response")
	}
	if !strings.Contains(logs.String(), response.Header().Get(RequestIDHeader)) {
		t.Fatal("panic log is missing the request id")
	}
}

func TestRecoverRepanicsAbortHandler(t *testing.T) {
	handler := Recover(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Fatal("http.ErrAbortHandler was swallowed")
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestAccessLogUsesRouteLabelAndStatus(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := AccessLog(logger, func(*http.Request) string { return "/v1/homes/{homeId}" })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/homes/0192-real-id", nil))

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["route"] != "/v1/homes/{homeId}" || entry["status"] != float64(http.StatusTeapot) {
		t.Fatalf("log entry = %v", entry)
	}
	if strings.Contains(logs.String(), "0192-real-id") {
		t.Fatal("raw path identifier leaked into access log")
	}
}

func TestNoStore(t *testing.T) {
	response := httptest.NewRecorder()
	NoStore(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing Cache-Control: no-store")
	}
}

func TestClientIP(t *testing.T) {
	var seen string
	handler := ClientIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = ClientIPFrom(r.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:54321"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if seen != "192.0.2.10" {
		t.Fatalf("client IP = %q, want the socket peer", seen)
	}
}

func TestLimitBody(t *testing.T) {
	var readErr error
	handler := LimitBody(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader("too long")))
	if readErr == nil {
		t.Fatal("expected reading past the limit to fail")
	}
}
