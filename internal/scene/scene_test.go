package scene_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"javis_api/internal/apitest"
	"javis_api/internal/testdb"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Run(m))
}

const movieNight = `{"icon":"sparkle","name":" Movie night ","actions":[
	{"type":"light","target":{"kind":"zone","key":"Living room"},"is_on":true,"brightness":0.3,"temperature":2700},
	{"type":"light","target":{"kind":"device","key":"bulb-1"},"is_on":false},
	{"type":"light","target":{"kind":"others"},"is_on":false},
	{"type":"lock_check"}]}`

type household struct {
	home                           string
	owner, admin, member, outsider apitest.Person
}

func setUp(a *apitest.API) household {
	h := household{owner: a.SignIn(), admin: a.SignIn(), member: a.SignIn(), outsider: a.SignIn()}
	h.home = a.CreateHome(h.owner)
	a.AddMember(h.home, h.admin, "admin")
	a.AddMember(h.home, h.member, "member")
	return h
}

func items(t *testing.T, r apitest.Response) []map[string]any {
	t.Helper()
	raw, _ := r.JSON(t)["items"].([]any)
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		value, _ := item.(map[string]any)
		result = append(result, value)
	}
	return result
}

func TestNewHomeStartsWithDefaultScenes(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	scenes := items(t, a.Do(http.MethodGet, "/v1/homes/"+h.home+"/scenes", h.member.Token, ""))
	want := []string{"Sleep", "Welcome home", "Leave home", "Wake up"}
	if len(scenes) != len(want) {
		t.Fatalf("scenes = %v", scenes)
	}
	for i, key := range want {
		if scenes[i]["preset_key"] != key || scenes[i]["name"] != nil || scenes[i]["last_run_at"] != nil {
			t.Fatalf("scene %d = %v", i, scenes[i])
		}
	}
}

func TestCreateUpdateRunDelete(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	scenes := "/v1/homes/" + h.home + "/scenes"

	created := a.Do(http.MethodPost, scenes, h.admin.Token, movieNight)
	created.Want(t, http.StatusCreated, "")
	body := created.JSON(t)
	id, _ := body["id"].(string)
	actions, _ := body["actions"].([]any)
	first, _ := actions[0].(map[string]any)
	if body["name"] != "Movie night" || len(actions) != 4 || first["brightness"] != .3 || first["temperature"] != float64(2700) {
		t.Fatalf("created = %v", body)
	}
	last, _ := actions[3].(map[string]any)
	if len(last) != 1 || last["type"] != "lock_check" {
		t.Fatalf("lock_check round trip = %v", last)
	}

	updated := a.Do(http.MethodPut, scenes+"/"+id, h.owner.Token, `{"icon":"moon","name":"Late","actions":[]}`)
	updated.Want(t, http.StatusOK, "")
	if body := updated.JSON(t); body["icon"] != "moon" || body["name"] != "Late" {
		t.Fatalf("updated = %v", body)
	}

	run := a.Do(http.MethodPost, scenes+"/"+id+"/runs", h.member.Token, "")
	run.Want(t, http.StatusOK, "")
	if run.JSON(t)["last_run_at"] == nil {
		t.Fatal("a run must be recorded")
	}

	a.Do(http.MethodDelete, scenes+"/"+id, h.admin.Token, "").Want(t, http.StatusNoContent, "")
	a.Do(http.MethodDelete, scenes+"/"+id, h.admin.Token, "").Want(t, http.StatusNotFound, "scene_not_found")
}

func TestPermissions(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	scenes := "/v1/homes/" + h.home + "/scenes"
	id, _ := a.Do(http.MethodPost, scenes, h.owner.Token, movieNight).JSON(t)["id"].(string)

	a.Do(http.MethodPost, scenes, h.member.Token, movieNight).Want(t, http.StatusForbidden, "forbidden")
	a.Do(http.MethodPut, scenes+"/"+id, h.member.Token, movieNight).Want(t, http.StatusForbidden, "forbidden")
	a.Do(http.MethodDelete, scenes+"/"+id, h.member.Token, "").Want(t, http.StatusForbidden, "forbidden")

	a.Do(http.MethodGet, scenes, h.outsider.Token, "").Want(t, http.StatusNotFound, "home_not_found")
	a.Do(http.MethodPost, scenes, h.outsider.Token, movieNight).Want(t, http.StatusNotFound, "home_not_found")
	a.Do(http.MethodPost, scenes+"/"+id+"/runs", h.outsider.Token, "").Want(t, http.StatusNotFound, "home_not_found")

	a.Do(http.MethodGet, scenes, "", "").Want(t, http.StatusUnauthorized, "unauthorized")
}

func TestSceneFromAnotherHomeIsNotFound(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	id, _ := a.Do(http.MethodPost, "/v1/homes/"+h.home+"/scenes", h.owner.Token, movieNight).JSON(t)["id"].(string)
	other := a.SignIn()
	scenes := "/v1/homes/" + a.CreateHome(other) + "/scenes/" + id
	a.Do(http.MethodPut, scenes, other.Token, movieNight).Want(t, http.StatusNotFound, "scene_not_found")
	a.Do(http.MethodDelete, scenes, other.Token, "").Want(t, http.StatusNotFound, "scene_not_found")
	a.Do(http.MethodPost, scenes+"/runs", other.Token, "").Want(t, http.StatusNotFound, "scene_not_found")
}

func TestValidation(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	scenes := "/v1/homes/" + h.home + "/scenes"
	wrap := func(action string) string { return `{"icon":"moon","name":"x","actions":[` + action + `]}` }
	for _, body := range []string{
		`{"icon":"rocket","name":"x","actions":[]}`,
		`{"icon":"moon","actions":[]}`,
		`{"icon":"moon","name":"x","preset_key":"Sleep","actions":[]}`,
		`{"icon":"moon","preset_key":"Party","actions":[]}`,
		`{"icon":"moon","name":"  ","actions":[]}`,
		wrap(`{"type":"unlock"}`),
		wrap(`{"type":"light"}`),
		wrap(`{"type":"light","target":{"kind":"all"}}`),
		wrap(`{"type":"light","target":{"kind":"room"},"is_on":true}`),
		wrap(`{"type":"light","target":{"kind":"zone"},"is_on":true}`),
		wrap(`{"type":"light","target":{"kind":"all","key":"x"},"is_on":true}`),
		wrap(`{"type":"light","target":{"kind":"all"},"is_on":true,"brightness":0}`),
		wrap(`{"type":"light","target":{"kind":"all"},"is_on":true,"brightness":1.5}`),
		wrap(`{"type":"light","target":{"kind":"all"},"is_on":true,"temperature":500}`),
		wrap(`{"type":"lock_check","is_on":false}`),
		wrap(strings.TrimSuffix(strings.Repeat(`{"type":"lock_check"},`, 51), ",")),
	} {
		a.Do(http.MethodPost, scenes, h.owner.Token, body).Want(t, http.StatusBadRequest, "invalid_scene")
	}
	a.Do(http.MethodPut, scenes+"/"+uuid.NewString(), h.owner.Token, `{"icon":"rocket","name":"x","actions":[]}`).
		Want(t, http.StatusBadRequest, "invalid_scene")
}

func TestSceneLimitHoldsUnderParallelRequests(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	scenes := "/v1/homes/" + h.home + "/scenes"
	for range 42 { // Four default scenes already exist.
		a.Do(http.MethodPost, scenes, h.owner.Token, movieNight).Want(t, http.StatusCreated, "")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			a.Do(http.MethodPost, scenes, h.owner.Token, movieNight)
		})
	}
	wg.Wait()
	if rows := a.Count(`SELECT count(*) FROM javis.scenes`); rows != 50 {
		t.Fatalf("scenes = %d, want 50", rows)
	}
	a.Do(http.MethodPost, scenes, h.owner.Token, movieNight).Want(t, http.StatusConflict, "scene_limit_reached")
}

func TestFailedSeedLeavesNoHome(t *testing.T) {
	a := apitest.New(t)
	owner := a.SignIn()
	ctx := t.Context()
	if _, err := a.Pool.Exec(ctx, `ALTER TABLE javis.scenes ADD CONSTRAINT no_scenes CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = a.Pool.Exec(context.Background(), `ALTER TABLE javis.scenes DROP CONSTRAINT no_scenes`)
	})
	r := a.Do(http.MethodPost, "/v1/homes", owner.Token, `{"name":"บ้าน","zones":[]}`)
	r.Want(t, http.StatusInternalServerError, "")
	if homes := a.Count(`SELECT count(*) FROM javis.homes`); homes != 0 {
		t.Fatalf("homes = %d", homes)
	}
}
