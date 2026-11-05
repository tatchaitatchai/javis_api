package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"
)

// RequestIDHeader carries the request id on requests and responses.
const RequestIDHeader = "X-Request-Id"

type requestIDKey struct{}

// RequestID reuses a well-formed inbound X-Request-Id (for example from a
// proxy) or generates one, stores it in the context and echoes it back.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom returns the request id stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// validRequestID accepts 1-64 characters from [A-Za-z0-9._-] so untrusted
// ids cannot inject anything into logs or headers.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !isAlphaNum && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error.
	return hex.EncodeToString(b[:])
}

type clientIPKey struct{}

// ClientIP stores the socket peer address in the context. Behind a proxy this
// is the proxy's address until a deployment defines its trusted proxy.
func ClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, host)))
	})
}

// ClientIPFrom returns the address stored by ClientIP, or "".
func ClientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

// LimitBody caps request bodies so a client cannot make the server buffer
// unbounded JSON. Reading past the limit fails the decode as a bad request.
func LimitBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// NoStore marks every response as non-cacheable. API responses carry
// household state and must not be stored by intermediaries.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Recover turns a handler panic into a 500 error envelope and logs it with
// the request id. http.ErrAbortHandler is re-panicked as net/http expects.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					"request_id", RequestIDFrom(r.Context()),
					"panic_type", fmt.Sprintf("%T", recovered),
					"stack", string(debug.Stack()))
				WriteError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog logs one line per request. routeLabel must return a route
// template (for example "/v1/homes/{homeId}"), never the raw path, so
// identifiers in URLs are not written to logs.
func AccessLog(logger *slog.Logger, routeLabel func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			logger.InfoContext(r.Context(), "http request",
				"request_id", RequestIDFrom(r.Context()),
				"method", r.Method,
				"route", routeLabel(r),
				"status", recorder.status,
				"duration_ms", time.Since(started).Milliseconds())
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if !s.wroteHeader {
		s.status = status
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
