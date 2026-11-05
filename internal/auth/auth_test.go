package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"javis_api/internal/auth"
	"javis_api/internal/httpapi"
	"javis_api/internal/testdb"
)

const (
	phone    = "+66812345678"
	other    = "+66891234567"
	clientIP = "192.0.2.1"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Run(m))
}

type fakeSender struct {
	mu   sync.Mutex
	last string
	sent int
	err  error
}

func (f *fakeSender) Send(_ context.Context, _, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.last, f.sent = code, f.sent+1
	return nil
}

func (f *fakeSender) code() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

type api struct {
	t      *testing.T
	pool   *pgxpool.Pool
	sender *fakeSender
	router http.Handler
}

func newAPI(t *testing.T, exposeCode bool) *api {
	t.Helper()
	pool := testdb.Pool(t)
	sender := &fakeSender{}
	service := auth.NewService(pool, auth.Options{
		Pepper:     []byte("test-pepper-test-pepper-test-pepper"),
		Sender:     sender,
		ExposeCode: exposeCode,
	})
	return &api{t: t, pool: pool, sender: sender, router: routerFor(service)}
}

func routerFor(service *auth.Service) http.Handler {
	return httpapi.NewHandler(httpapi.Dependencies{
		Logger:        slog.New(slog.DiscardHandler),
		Ping:          func(context.Context) error { return nil },
		HealthTimeout: time.Second,
		Auth:          auth.NewHandler(service),
	})
}

type response struct {
	*httptest.ResponseRecorder
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %s", r.Body)
	}
	return body
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	body, ok := r.json(t)["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error: %s", r.Body)
	}
	code, _ := body["code"].(string)
	return code
}

func (a *api) do(method, path, token, body string) response {
	return a.doFrom(clientIP, method, path, token, body)
}

func (a *api) doFrom(ip, method, path, token, body string) response {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = ip + ":1234"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	a.router.ServeHTTP(recorder, request)
	return response{recorder}
}

func (a *api) requestCode(number string) response {
	return a.do(http.MethodPost, "/v1/auth/otp/request", "", `{"phone":"`+number+`"}`)
}

func (a *api) verify(number, code string) response {
	return a.do(http.MethodPost, "/v1/auth/otp/verify", "", `{"phone":"`+number+`","code":"`+code+`"}`)
}

func (a *api) signIn(number string) string {
	a.t.Helper()
	if r := a.requestCode(number); r.Code != http.StatusAccepted {
		a.t.Fatalf("request code: HTTP %d %s", r.Code, r.Body)
	}
	r := a.verify(number, a.sender.code())
	if r.Code != http.StatusOK {
		a.t.Fatalf("verify: HTTP %d %s", r.Code, r.Body)
	}
	token, _ := r.json(a.t)["token"].(string)
	return token
}

// allowResend ages challenges past the resend window without sleeping.
func (a *api) allowResend() {
	a.t.Helper()
	a.exec(`UPDATE javis.otp_challenges SET created_at = created_at - interval '46 seconds'`)
}

func (a *api) exec(sql string) {
	a.t.Helper()
	if _, err := a.pool.Exec(a.t.Context(), sql); err != nil {
		a.t.Fatal(err)
	}
}

func (a *api) count(sql string) int {
	a.t.Helper()
	var n int
	if err := a.pool.QueryRow(a.t.Context(), sql).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

func wrongCode(right string) string {
	if right == "000000" {
		return "000001"
	}
	return "000000"
}

func TestSignInFlow(t *testing.T) {
	a := newAPI(t, false)

	first := a.requestCode(phone)
	if first.Code != http.StatusAccepted {
		t.Fatalf("HTTP %d %s", first.Code, first.Body)
	}
	body := first.json(t)
	if body["resend_after_seconds"] != float64(45) || body["expires_in_seconds"] != float64(300) {
		t.Fatalf("challenge = %v", body)
	}
	if _, present := body["debug_code"]; present {
		t.Fatal("debug_code must be absent outside debug mode")
	}

	verified := a.verify(phone, a.sender.code())
	if verified.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", verified.Code, verified.Body)
	}
	session := verified.json(t)
	token, _ := session["token"].(string)
	user, _ := session["user"].(map[string]any)
	if token == "" || user["phone"] != phone || user["name"] != nil {
		t.Fatalf("session = %v", session)
	}

	if r := a.do(http.MethodPatch, "/v1/me", token, `{"name":"  มายด์  "}`); r.Code != http.StatusOK || r.json(t)["name"] != "มายด์" {
		t.Fatalf("set name: HTTP %d %s", r.Code, r.Body)
	}

	a.allowResend()
	second := a.signIn(phone)
	me := a.do(http.MethodGet, "/v1/me", second, "")
	if me.Code != http.StatusOK || me.json(t)["id"] != user["id"] || me.json(t)["name"] != "มายด์" {
		t.Fatalf("second sign-in did not reuse the account: %s", me.Body)
	}

	if r := a.do(http.MethodPost, "/v1/auth/logout", token, ""); r.Code != http.StatusNoContent {
		t.Fatalf("logout: HTTP %d", r.Code)
	}
	if r := a.do(http.MethodGet, "/v1/me", token, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: HTTP %d", r.Code)
	}
	if r := a.do(http.MethodGet, "/v1/me", second, ""); r.Code != http.StatusOK {
		t.Fatalf("logout must revoke only its own session: HTTP %d", r.Code)
	}
}

func TestRequestDoesNotRevealAccounts(t *testing.T) {
	a := newAPI(t, false)
	a.signIn(phone)
	a.allowResend()
	existing, unknown := a.requestCode(phone), a.requestCode(other)
	if existing.Code != unknown.Code || existing.Body.String() != unknown.Body.String() {
		t.Fatalf("responses differ: %s / %s", existing.Body, unknown.Body)
	}
}

func TestDebugCodeSignsIn(t *testing.T) {
	a := newAPI(t, true)
	r := a.requestCode(phone)
	code, _ := r.json(t)["debug_code"].(string)
	if len(code) != 6 {
		t.Fatalf("debug_code = %q", code)
	}
	if v := a.verify(phone, code); v.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", v.Code, v.Body)
	}
}

func TestCodesThatMustFail(t *testing.T) {
	t.Run("wrong", func(t *testing.T) {
		a := newAPI(t, false)
		a.requestCode(phone)
		if r := a.verify(phone, wrongCode(a.sender.code())); r.Code != http.StatusUnauthorized || r.errorCode(t) != "invalid_code" {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
	})
	t.Run("no challenge", func(t *testing.T) {
		a := newAPI(t, false)
		if r := a.verify(phone, "123456"); r.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
	})
	t.Run("other number", func(t *testing.T) {
		a := newAPI(t, false)
		a.requestCode(phone)
		if r := a.verify(other, a.sender.code()); r.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
	})
	t.Run("expired", func(t *testing.T) {
		a := newAPI(t, false)
		a.requestCode(phone)
		a.exec(`UPDATE javis.otp_challenges SET expires_at = now() - interval '1 second'`)
		if r := a.verify(phone, a.sender.code()); r.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
	})
	t.Run("reused", func(t *testing.T) {
		a := newAPI(t, false)
		a.requestCode(phone)
		code := a.sender.code()
		if r := a.verify(phone, code); r.Code != http.StatusOK {
			t.Fatalf("HTTP %d", r.Code)
		}
		if r := a.verify(phone, code); r.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
	})
	t.Run("superseded by a newer code", func(t *testing.T) {
		a := newAPI(t, false)
		a.requestCode(phone)
		old := a.sender.code()
		a.allowResend()
		a.requestCode(phone)
		if old == a.sender.code() {
			t.Skip("both random codes matched")
		}
		if r := a.verify(phone, old); r.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
	})
}

func TestCorrectCodeFailsAfterFiveWrongAttempts(t *testing.T) {
	a := newAPI(t, false)
	a.requestCode(phone)
	code := a.sender.code()
	for range 5 {
		if r := a.verify(phone, wrongCode(code)); r.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
	}
	if r := a.verify(phone, code); r.Code != http.StatusTooManyRequests || r.errorCode(t) != "too_many_attempts" {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
}

func TestParallelGuessesCannotExceedAttemptLimit(t *testing.T) {
	a := newAPI(t, false)
	a.requestCode(phone)
	wrong := wrongCode(a.sender.code())
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { a.verify(phone, wrong) })
	}
	wg.Wait()
	if attempts := a.count(`SELECT attempts FROM javis.otp_challenges`); attempts != 5 {
		t.Fatalf("attempts = %d, want 5", attempts)
	}
}

func TestSendLimits(t *testing.T) {
	t.Run("resend window", func(t *testing.T) {
		a := newAPI(t, false)
		a.requestCode(phone)
		r := a.requestCode(phone)
		retry := r.Header().Get("Retry-After")
		if r.Code != http.StatusTooManyRequests || r.errorCode(t) != "rate_limited" || retry == "" || retry == "0" {
			t.Fatalf("HTTP %d Retry-After %q %s", r.Code, retry, r.Body)
		}
		if a.sender.sent != 1 {
			t.Fatalf("sent = %d, want 1", a.sender.sent)
		}
	})
	t.Run("per number per hour", func(t *testing.T) {
		a := newAPI(t, false)
		for range 5 {
			if r := a.requestCode(phone); r.Code != http.StatusAccepted {
				t.Fatalf("HTTP %d %s", r.Code, r.Body)
			}
			a.allowResend()
		}
		r := a.requestCode(phone)
		if r.Code != http.StatusTooManyRequests || r.Header().Get("Retry-After") != "3600" {
			t.Fatalf("HTTP %d Retry-After %q", r.Code, r.Header().Get("Retry-After"))
		}
		a.exec(`UPDATE javis.otp_challenges SET created_at = created_at - interval '1 hour'`)
		if r := a.requestCode(phone); r.Code != http.StatusAccepted {
			t.Fatalf("limit did not reset: HTTP %d", r.Code)
		}
	})
	t.Run("per client IP per hour", func(t *testing.T) {
		a := newAPI(t, false)
		for i := range 20 {
			number := fmt.Sprintf("+66810000%03d", i)
			if r := a.requestCode(number); r.Code != http.StatusAccepted {
				t.Fatalf("number %d: HTTP %d %s", i, r.Code, r.Body)
			}
		}
		if r := a.requestCode(other); r.Code != http.StatusTooManyRequests {
			t.Fatalf("HTTP %d %s", r.Code, r.Body)
		}
		r := a.doFrom("192.0.2.2", http.MethodPost, "/v1/auth/otp/request", "", `{"phone":"`+other+`"}`)
		if r.Code != http.StatusAccepted {
			t.Fatalf("another IP must not be limited: HTTP %d %s", r.Code, r.Body)
		}
	})
	t.Run("parallel requests send one code", func(t *testing.T) {
		a := newAPI(t, false)
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() { a.requestCode(phone) })
		}
		wg.Wait()
		if rows := a.count(`SELECT count(*) FROM javis.otp_challenges`); rows != 1 || a.sender.sent != 1 {
			t.Fatalf("challenges = %d, sent = %d", rows, a.sender.sent)
		}
	})
}

func TestFailedSMSDoesNotCountAgainstTheUser(t *testing.T) {
	a := newAPI(t, false)
	a.sender.err = errors.New("provider down")
	if r := a.requestCode(phone); r.Code != http.StatusServiceUnavailable || r.errorCode(t) != "sms_unavailable" {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
	a.sender.err = nil
	if r := a.requestCode(phone); r.Code != http.StatusAccepted {
		t.Fatalf("retry after SMS failure: HTTP %d %s", r.Code, r.Body)
	}
}

func TestInputValidation(t *testing.T) {
	a := newAPI(t, false)
	for _, number := range []string{"", "0812345678", "+66712345678", "+6681234567", "+1812345678"} {
		if r := a.requestCode(number); r.Code != http.StatusBadRequest || r.errorCode(t) != "invalid_phone" {
			t.Fatalf("%q: HTTP %d %s", number, r.Code, r.Body)
		}
	}
	for _, code := range []string{"", "12345", "1234567", "12345a"} {
		if r := a.verify(phone, code); r.Code != http.StatusBadRequest || r.errorCode(t) != "invalid_request" {
			t.Fatalf("%q: HTTP %d %s", code, r.Code, r.Body)
		}
	}
	if r := a.do(http.MethodPost, "/v1/auth/otp/request", "", `{not json`); r.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: HTTP %d", r.Code)
	}
	huge := `{"phone":"` + strings.Repeat("9", 70_000) + `"}`
	if r := a.do(http.MethodPost, "/v1/auth/otp/request", "", huge); r.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: HTTP %d", r.Code)
	}
	token := a.signIn(phone)
	for _, name := range []string{"", "   ", strings.Repeat("ก", 61), "a\nb"} {
		if r := a.do(http.MethodPatch, "/v1/me", token, `{"name":`+jsonString(t, name)+`}`); r.Code != http.StatusBadRequest || r.errorCode(t) != "invalid_name" {
			t.Fatalf("%q: HTTP %d %s", name, r.Code, r.Body)
		}
	}
	if r := a.do(http.MethodPatch, "/v1/me", token, `{"name":"`+strings.Repeat("ก", 60)+`"}`); r.Code != http.StatusOK {
		t.Fatalf("60 characters must fit: HTTP %d %s", r.Code, r.Body)
	}
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestProtectedRoutesRejectBadTokens(t *testing.T) {
	a := newAPI(t, false)
	token := a.signIn(phone)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1/me"},
		{http.MethodPatch, "/v1/me"},
		{http.MethodPost, "/v1/auth/logout"},
	}
	for _, route := range routes {
		for _, bad := range []string{"", "not-a-token"} {
			r := a.do(route.method, route.path, bad, `{"name":"x"}`)
			if r.Code != http.StatusUnauthorized || r.errorCode(t) != "unauthorized" || r.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("%s %s with %q: HTTP %d %s", route.method, route.path, bad, r.Code, r.Body)
			}
		}
	}
	a.exec(`UPDATE javis.sessions SET expires_at = now() - interval '1 second'`)
	if r := a.do(http.MethodGet, "/v1/me", token, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: HTTP %d", r.Code)
	}
}

func TestSessionExtendsOnUse(t *testing.T) {
	a := newAPI(t, false)
	token := a.signIn(phone)
	a.exec(`UPDATE javis.sessions SET last_used_at = now() - interval '2 hours', expires_at = now() + interval '1 day'`)
	if r := a.do(http.MethodGet, "/v1/me", token, ""); r.Code != http.StatusOK {
		t.Fatalf("HTTP %d", r.Code)
	}
	if extended := a.count(`SELECT count(*) FROM javis.sessions WHERE expires_at > now() + interval '89 days'`); extended != 1 {
		t.Fatal("session was not extended")
	}
}

func TestDatabaseOutageIsNotASignOut(t *testing.T) {
	a := newAPI(t, false)
	broken, err := pgxpool.New(t.Context(), "postgres://javis:x@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	a.router = routerFor(auth.NewService(broken, auth.Options{Sender: a.sender}))
	r := a.do(http.MethodGet, "/v1/me", "some-token", "")
	if r.Code != http.StatusServiceUnavailable || r.errorCode(t) != "service_unavailable" {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
}

func TestSecretsAreNotStored(t *testing.T) {
	a := newAPI(t, false)
	token := a.signIn(phone)
	var codeHash, tokenHash []byte
	if err := a.pool.QueryRow(t.Context(), `SELECT code_hash FROM javis.otp_challenges`).Scan(&codeHash); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(t.Context(), `SELECT token_hash FROM javis.sessions`).Scan(&tokenHash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(codeHash, []byte(a.sender.code())) || bytes.Contains(tokenHash, []byte(token)) {
		t.Fatal("raw code or token stored")
	}
}

func TestCleanupRemovesOldRows(t *testing.T) {
	a := newAPI(t, false)
	a.signIn(phone)
	a.signIn(other)
	a.exec(`UPDATE javis.otp_challenges SET created_at = now() - interval '2 days'
		WHERE phone = '` + phone + `'`)
	a.exec(`UPDATE javis.sessions SET expires_at = now() - interval '1 second'
		WHERE user_id = (SELECT id FROM javis.users WHERE phone = '` + phone + `')`)

	service := auth.NewService(a.pool, auth.Options{Sender: a.sender})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		service.RunCleanup(ctx, time.Hour, slog.New(slog.DiscardHandler))
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for a.count(`SELECT count(*) FROM javis.sessions`) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if challenges, sessions := a.count(`SELECT count(*) FROM javis.otp_challenges`), a.count(`SELECT count(*) FROM javis.sessions`); challenges != 1 || sessions != 1 {
		t.Fatalf("challenges = %d, sessions = %d, want 1 and 1", challenges, sessions)
	}
}
