package home_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"javis_api/internal/auth"
	"javis_api/internal/auth/smssender"
	"javis_api/internal/home"
	"javis_api/internal/httpapi"
	"javis_api/internal/testdb"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Run(m))
}

type api struct {
	t         *testing.T
	pool      *pgxpool.Pool
	router    http.Handler
	nextPhone int
}

func newAPI(t *testing.T) *api {
	t.Helper()
	pool := testdb.Pool(t)
	authService := auth.NewService(pool, auth.Options{
		Pepper:     []byte("test-pepper-test-pepper-test-pepper"),
		Sender:     smssender.Debug{},
		ExposeCode: true,
	})
	return &api{t: t, pool: pool, router: httpapi.NewHandler(httpapi.Dependencies{
		Logger:        slog.New(slog.DiscardHandler),
		Ping:          func(context.Context) error { return nil },
		HealthTimeout: time.Second,
		Auth:          auth.NewHandler(authService),
		Home:          home.NewHandler(home.NewService(pool)),
	})}
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
	body, _ := r.json(t)["error"].(map[string]any)
	code, _ := body["code"].(string)
	return code
}

func (a *api) do(method, path, token, body string) response {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	// One address per test user keeps sign-in rate limits out of these tests.
	request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", a.nextPhone)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	a.router.ServeHTTP(recorder, request)
	return response{recorder}
}

type person struct {
	token string
	id    string
}

func (a *api) signIn() person {
	a.t.Helper()
	a.nextPhone++
	phone := fmt.Sprintf("+66810000%03d", a.nextPhone)
	requested := a.do(http.MethodPost, "/v1/auth/otp/request", "", `{"phone":"`+phone+`"}`)
	code, _ := requested.json(a.t)["debug_code"].(string)
	verified := a.do(http.MethodPost, "/v1/auth/otp/verify", "", `{"phone":"`+phone+`","code":"`+code+`"}`)
	if verified.Code != http.StatusOK {
		a.t.Fatalf("sign in: HTTP %d %s", verified.Code, verified.Body)
	}
	body := verified.json(a.t)
	token, _ := body["token"].(string)
	user, _ := body["user"].(map[string]any)
	id, _ := user["id"].(string)
	return person{token: token, id: id}
}

func (a *api) createHome(owner person) string {
	a.t.Helper()
	r := a.do(http.MethodPost, "/v1/homes", owner.token, `{"name":"บ้านทดสอบ","zones":[{"preset_key":"Bedroom"}]}`)
	if r.Code != http.StatusCreated {
		a.t.Fatalf("create home: HTTP %d %s", r.Code, r.Body)
	}
	id, _ := r.json(a.t)["id"].(string)
	return id
}

func (a *api) addMember(homeID string, who person, role string) {
	a.t.Helper()
	a.exec(`INSERT INTO javis.home_members (home_id, user_id, role) VALUES ($1, $2, $3)`, homeID, who.id, role)
}

func (a *api) exec(sql string, args ...any) {
	a.t.Helper()
	if _, err := a.pool.Exec(a.t.Context(), sql, args...); err != nil {
		a.t.Fatal(err)
	}
}

func (a *api) count(sql string, args ...any) int {
	a.t.Helper()
	var n int
	if err := a.pool.QueryRow(a.t.Context(), sql, args...).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

func TestCreateListGet(t *testing.T) {
	a := newAPI(t)
	owner := a.signIn()

	empty := a.do(http.MethodGet, "/v1/homes", owner.token, "")
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty list: HTTP %d %s", empty.Code, empty.Body)
	}

	created := a.do(http.MethodPost, "/v1/homes", owner.token,
		`{"name":"  บ้านลาดพร้าว ","zones":[{"preset_key":"Living room"},{"name":" ห้องพระ "}]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("HTTP %d %s", created.Code, created.Body)
	}
	body := created.json(t)
	id, _ := body["id"].(string)
	zones, _ := body["zones"].([]any)
	if body["name"] != "บ้านลาดพร้าว" || body["role"] != "owner" || len(zones) != 2 {
		t.Fatalf("created = %v", body)
	}

	got := a.do(http.MethodGet, "/v1/homes/"+id, owner.token, "").json(t)
	gotZones, _ := got["zones"].([]any)
	first, _ := gotZones[0].(map[string]any)
	second, _ := gotZones[1].(map[string]any)
	if first["preset_key"] != "Living room" || first["name"] != nil || second["name"] != "ห้องพระ" || second["preset_key"] != nil {
		t.Fatalf("zones = %v", gotZones)
	}

	list := a.do(http.MethodGet, "/v1/homes", owner.token, "").json(t)
	items, _ := list["items"].([]any)
	summary, _ := items[0].(map[string]any)
	if len(items) != 1 || summary["zone_count"] != float64(2) || summary["member_count"] != float64(1) || summary["role"] != "owner" {
		t.Fatalf("list = %v", list)
	}
	if unlock := a.count(`SELECT count(*) FROM javis.home_members WHERE can_unlock AND role = 'owner'`); unlock != 1 {
		t.Fatal("the owner must be able to unlock")
	}
}

func TestPermissionsByRole(t *testing.T) {
	const allowed, denied = "allowed", "denied"
	operations := []struct {
		name                         string
		method, suffix, body         string
		success                      int
		nonMember, member, admin, ow string
	}{
		{"read", http.MethodGet, "", "", http.StatusOK, "404", allowed, allowed, allowed},
		{"rename", http.MethodPatch, "", `{"name":"ใหม่"}`, http.StatusOK, "404", denied, allowed, allowed},
		{"add zone", http.MethodPost, "/zones", `{"name":"โซนใหม่"}`, http.StatusCreated, "404", denied, allowed, allowed},
		{"delete", http.MethodDelete, "", "", http.StatusNoContent, "404", denied, denied, allowed},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			a := newAPI(t)
			owner, admin, member, stranger := a.signIn(), a.signIn(), a.signIn(), a.signIn()
			homeID := a.createHome(owner)
			a.addMember(homeID, admin, "admin")
			a.addMember(homeID, member, "member")
			path := "/v1/homes/" + homeID + op.suffix

			check := func(who person, want string) {
				t.Helper()
				r := a.do(op.method, path, who.token, op.body)
				switch want {
				case allowed:
					if r.Code != op.success {
						t.Fatalf("HTTP %d %s, want %d", r.Code, r.Body, op.success)
					}
				case denied:
					if r.Code != http.StatusForbidden || r.errorCode(t) != "forbidden" {
						t.Fatalf("HTTP %d %s, want 403", r.Code, r.Body)
					}
				default:
					if r.Code != http.StatusNotFound || r.errorCode(t) != "home_not_found" {
						t.Fatalf("HTTP %d %s, want 404", r.Code, r.Body)
					}
				}
			}
			check(stranger, op.nonMember)
			check(member, op.member)
			check(admin, op.admin)
			check(owner, op.ow) // Last: delete removes the home.
		})
	}
}

func TestHomesAreVisibleOnlyToMembers(t *testing.T) {
	a := newAPI(t)
	owner, stranger := a.signIn(), a.signIn()
	a.createHome(owner)
	r := a.do(http.MethodGet, "/v1/homes", stranger.token, "")
	if strings.TrimSpace(r.Body.String()) != `{"items":[]}` {
		t.Fatalf("stranger sees: %s", r.Body)
	}
}

func TestRoutesRequireASession(t *testing.T) {
	a := newAPI(t)
	id := uuid.NewString()
	for _, route := range [][2]string{
		{http.MethodGet, "/v1/homes"}, {http.MethodPost, "/v1/homes"},
		{http.MethodGet, "/v1/homes/" + id}, {http.MethodPatch, "/v1/homes/" + id},
		{http.MethodDelete, "/v1/homes/" + id}, {http.MethodPost, "/v1/homes/" + id + "/zones"},
	} {
		if r := a.do(route[0], route[1], "", `{}`); r.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: HTTP %d %s", route[0], route[1], r.Code, r.Body)
		}
	}
}

func TestMalformedHomeIDIsRejected(t *testing.T) {
	a := newAPI(t)
	owner := a.signIn()
	if r := a.do(http.MethodGet, "/v1/homes/not-a-uuid", owner.token, ""); r.Code != http.StatusBadRequest || r.errorCode(t) != "invalid_request" {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
}

func TestValidation(t *testing.T) {
	a := newAPI(t)
	owner := a.signIn()
	long := strings.Repeat("ก", 61)
	for _, body := range []string{
		`{"name":"","zones":[]}`,
		`{"name":"   ","zones":[]}`,
		`{"name":"` + long + `","zones":[]}`,
		`{"name":"a\nb","zones":[]}`,
		`{"name":"บ้าน","zones":[{}]}`,
		`{"name":"บ้าน","zones":[{"preset_key":"Bedroom","name":"x"}]}`,
		`{"name":"บ้าน","zones":[{"preset_key":"Garage"}]}`,
		`{"name":"บ้าน","zones":[{"name":"  "}]}`,
		`{"name":"บ้าน","zones":[{"name":"` + long + `"}]}`,
	} {
		if r := a.do(http.MethodPost, "/v1/homes", owner.token, body); r.Code != http.StatusBadRequest || r.errorCode(t) != "invalid_home" {
			t.Fatalf("%s: HTTP %d %s", body, r.Code, r.Body)
		}
	}
	tooMany := `{"name":"บ้าน","zones":[` + strings.Repeat(`{"preset_key":"Bedroom"},`, 50) + `{"preset_key":"Bedroom"}]}`
	if r := a.do(http.MethodPost, "/v1/homes", owner.token, tooMany); r.Code != http.StatusBadRequest {
		t.Fatalf("51 zones: HTTP %d %s", r.Code, r.Body)
	}
	if rows := a.count(`SELECT count(*) FROM javis.homes`); rows != 0 {
		t.Fatalf("rejected requests left %d homes", rows)
	}

	homeID := a.createHome(owner)
	for _, body := range []string{`{}`, `{"preset_key":"Garage"}`, `{"name":" "}`, `{"preset_key":"Office","name":"x"}`} {
		if r := a.do(http.MethodPost, "/v1/homes/"+homeID+"/zones", owner.token, body); r.Code != http.StatusBadRequest || r.errorCode(t) != "invalid_zone" {
			t.Fatalf("%s: HTTP %d %s", body, r.Code, r.Body)
		}
	}
	if r := a.do(http.MethodPatch, "/v1/homes/"+homeID, owner.token, `{"name":" "}`); r.Code != http.StatusBadRequest || r.errorCode(t) != "invalid_home" {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
}

func TestDeleteRemovesEverythingInTheHome(t *testing.T) {
	a := newAPI(t)
	owner, member := a.signIn(), a.signIn()
	homeID := a.createHome(owner)
	a.addMember(homeID, member, "member")
	if r := a.do(http.MethodDelete, "/v1/homes/"+homeID, owner.token, ""); r.Code != http.StatusNoContent {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
	if r := a.do(http.MethodGet, "/v1/homes/"+homeID, member.token, ""); r.Code != http.StatusNotFound {
		t.Fatalf("HTTP %d", r.Code)
	}
	if zones, members := a.count(`SELECT count(*) FROM javis.zones`), a.count(`SELECT count(*) FROM javis.home_members`); zones != 0 || members != 0 {
		t.Fatalf("zones = %d, members = %d", zones, members)
	}
}

func TestZonesKeepTheirOrder(t *testing.T) {
	a := newAPI(t)
	owner := a.signIn()
	homeID := a.createHome(owner)
	for _, name := range []string{"หนึ่ง", "สอง"} {
		a.do(http.MethodPost, "/v1/homes/"+homeID+"/zones", owner.token, `{"name":"`+name+`"}`)
	}
	zones, _ := a.do(http.MethodGet, "/v1/homes/"+homeID, owner.token, "").json(t)["zones"].([]any)
	last, _ := zones[2].(map[string]any)
	if len(zones) != 3 || last["name"] != "สอง" {
		t.Fatalf("zones = %v", zones)
	}
}

func TestHomeLimitHoldsUnderParallelRequests(t *testing.T) {
	a := newAPI(t)
	owner := a.signIn()
	for range 18 {
		a.createHome(owner)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			a.do(http.MethodPost, "/v1/homes", owner.token, `{"name":"บ้าน","zones":[]}`)
		})
	}
	wg.Wait()
	if homes := a.count(`SELECT count(*) FROM javis.homes`); homes != 20 {
		t.Fatalf("homes = %d, want 20", homes)
	}
	if r := a.do(http.MethodPost, "/v1/homes", owner.token, `{"name":"บ้าน","zones":[]}`); r.Code != http.StatusConflict || r.errorCode(t) != "home_limit_reached" {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
}

func TestZoneLimitHoldsUnderParallelRequests(t *testing.T) {
	a := newAPI(t)
	owner := a.signIn()
	homeID := a.createHome(owner) // Starts with one zone.
	for range 47 {
		a.do(http.MethodPost, "/v1/homes/"+homeID+"/zones", owner.token, `{"preset_key":"Office"}`)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			a.do(http.MethodPost, "/v1/homes/"+homeID+"/zones", owner.token, `{"preset_key":"Office"}`)
		})
	}
	wg.Wait()
	if zones := a.count(`SELECT count(*) FROM javis.zones`); zones != 50 {
		t.Fatalf("zones = %d, want 50", zones)
	}
	r := a.do(http.MethodPost, "/v1/homes/"+homeID+"/zones", owner.token, `{"preset_key":"Office"}`)
	if r.Code != http.StatusConflict || r.errorCode(t) != "zone_limit_reached" {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
}

func TestCreateLeavesNothingBehindOnFailure(t *testing.T) {
	a := newAPI(t)
	owner := a.signIn()
	a.exec(`CREATE FUNCTION javis.fail_zone() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'injected failure'; END $$ LANGUAGE plpgsql`)
	a.exec(`CREATE TRIGGER fail_zone BEFORE INSERT ON javis.zones FOR EACH ROW EXECUTE FUNCTION javis.fail_zone()`)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = a.pool.Exec(ctx, `DROP TRIGGER fail_zone ON javis.zones`)
		_, _ = a.pool.Exec(ctx, `DROP FUNCTION javis.fail_zone()`)
	})
	r := a.do(http.MethodPost, "/v1/homes", owner.token, `{"name":"บ้าน","zones":[{"preset_key":"Bedroom"}]}`)
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("HTTP %d %s", r.Code, r.Body)
	}
	if homes, members := a.count(`SELECT count(*) FROM javis.homes`), a.count(`SELECT count(*) FROM javis.home_members`); homes != 0 || members != 0 {
		t.Fatalf("homes = %d, members = %d", homes, members)
	}
}

func TestDatabaseRulesHoldEvenIfCodeIsWrong(t *testing.T) {
	a := newAPI(t)
	owner, other := a.signIn(), a.signIn()
	homeID := a.createHome(owner)
	ctx := t.Context()
	if _, err := a.pool.Exec(ctx, `INSERT INTO javis.home_members (home_id, user_id, role) VALUES ($1, $2, 'owner')`, homeID, other.id); err == nil {
		t.Fatal("a second owner must be impossible")
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO javis.zones (id, home_id, position) VALUES ($1, $2, 9)`, uuid.New(), homeID); err == nil {
		t.Fatal("a zone with neither preset nor name must be impossible")
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO javis.zones (id, home_id, preset_key, name, position) VALUES ($1, $2, 'Office', 'x', 9)`, uuid.New(), homeID); err == nil {
		t.Fatal("a zone with both preset and name must be impossible")
	}
}
