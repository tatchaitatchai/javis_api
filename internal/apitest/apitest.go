// Package apitest drives the whole API through its router against a real
// database, for tests of modules that sit on top of homes.
package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"javis_api/internal/auth"
	"javis_api/internal/auth/smssender"
	"javis_api/internal/device"
	"javis_api/internal/home"
	"javis_api/internal/httpapi"
	"javis_api/internal/scene"
	"javis_api/internal/testdb"
)

// API is a router wired the way cmd/api wires it.
type API struct {
	t       *testing.T
	Pool    *pgxpool.Pool
	router  http.Handler
	signIns int
}

func New(t *testing.T) *API {
	t.Helper()
	pool := testdb.Pool(t)
	authService := auth.NewService(pool, auth.Options{
		Pepper:     []byte("test-pepper-test-pepper-test-pepper"),
		Sender:     smssender.Debug{},
		ExposeCode: true,
	})
	scenes := scene.NewService(pool)
	homes := home.NewService(pool, scenes)
	scenes.UseAccess(homes)
	return &API{t: t, Pool: pool, router: httpapi.NewHandler(httpapi.Dependencies{
		Logger:        slog.New(slog.DiscardHandler),
		Ping:          func(context.Context) error { return nil },
		HealthTimeout: time.Second,
		Auth:          auth.NewHandler(authService),
		Home:          home.NewHandler(homes),
		Device:        device.NewHandler(device.NewService(pool, homes)),
		Scene:         scene.NewHandler(scenes),
	})}
}

// Person is a signed-in user.
type Person struct {
	Token string
	ID    string
}

// Response is a recorded HTTP response.
type Response struct {
	*httptest.ResponseRecorder
}

// JSON decodes the body as an object.
func (r Response) JSON(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %s", r.Body)
	}
	return body
}

// Want fails the test unless the status, and the error code if given, match.
func (r Response) Want(t *testing.T, status int, code string) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("HTTP %d %s, want %d", r.Code, r.Body, status)
	}
	if code == "" {
		return
	}
	body, _ := r.JSON(t)["error"].(map[string]any)
	if body["code"] != code {
		t.Fatalf("error = %s, want %s", r.Body, code)
	}
}

func (a *API) Do(method, path, token, body string) Response {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", a.signIns)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	a.router.ServeHTTP(recorder, request)
	return Response{recorder}
}

func (a *API) SignIn() Person {
	a.t.Helper()
	a.signIns++
	phone := fmt.Sprintf("+66810000%03d", a.signIns)
	requested := a.Do(http.MethodPost, "/v1/auth/otp/request", "", `{"phone":"`+phone+`"}`)
	code, _ := requested.JSON(a.t)["debug_code"].(string)
	verified := a.Do(http.MethodPost, "/v1/auth/otp/verify", "", `{"phone":"`+phone+`","code":"`+code+`"}`)
	verified.Want(a.t, http.StatusOK, "")
	body := verified.JSON(a.t)
	token, _ := body["token"].(string)
	user, _ := body["user"].(map[string]any)
	id, _ := user["id"].(string)
	return Person{Token: token, ID: id}
}

func (a *API) CreateHome(owner Person) string {
	a.t.Helper()
	r := a.Do(http.MethodPost, "/v1/homes", owner.Token, `{"name":"บ้านทดสอบ","zones":[{"preset_key":"Bedroom"}]}`)
	r.Want(a.t, http.StatusCreated, "")
	id, _ := r.JSON(a.t)["id"].(string)
	return id
}

func (a *API) AddMember(homeID string, who Person, role string) {
	a.t.Helper()
	_, err := a.Pool.Exec(a.t.Context(),
		`INSERT INTO javis.home_members (home_id, user_id, role) VALUES ($1, $2, $3)`, homeID, who.ID, role)
	if err != nil {
		a.t.Fatal(err)
	}
}

func (a *API) Count(sql string, args ...any) int {
	a.t.Helper()
	var n int
	if err := a.Pool.QueryRow(a.t.Context(), sql, args...).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}
