package device_test

import (
	"fmt"
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

const claimBody = `{"provider":"tuya","external_ref":"bulb-1","kind":"light","name":" Ceiling "}`

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

func claim(t *testing.T, a *apitest.API, h household) string {
	t.Helper()
	r := a.Do(http.MethodPost, "/v1/homes/"+h.home+"/devices", h.owner.Token, claimBody)
	r.Want(t, http.StatusCreated, "")
	id, _ := r.JSON(t)["id"].(string)
	return id
}

func TestClaimListUpdateRemove(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	devices := "/v1/homes/" + h.home + "/devices"

	created := a.Do(http.MethodPost, devices, h.admin.Token, claimBody)
	created.Want(t, http.StatusCreated, "")
	body := created.JSON(t)
	id, _ := body["id"].(string)
	if body["name"] != "Ceiling" || body["external_ref"] != "bulb-1" || body["zone_id"] != nil {
		t.Fatalf("created = %v", body)
	}

	again := a.Do(http.MethodPost, devices, h.owner.Token, claimBody)
	again.Want(t, http.StatusOK, "")
	if again.JSON(t)["id"] != id {
		t.Fatal("claiming twice must return the same device")
	}

	home := a.Do(http.MethodGet, "/v1/homes/"+h.home, h.owner.Token, "").JSON(t)
	zones, _ := home["zones"].([]any)
	zone, _ := zones[0].(map[string]any)
	zoneID, _ := zone["id"].(string)
	updated := a.Do(http.MethodPatch, devices+"/"+id, h.admin.Token, `{"name":"Lamp","zone_id":"`+zoneID+`"}`)
	updated.Want(t, http.StatusOK, "")
	if body := updated.JSON(t); body["name"] != "Lamp" || body["zone_id"] != zoneID {
		t.Fatalf("updated = %v", body)
	}

	list := a.Do(http.MethodGet, devices, h.member.Token, "").JSON(t)
	items, _ := list["items"].([]any)
	first, _ := items[0].(map[string]any)
	if len(items) != 1 || first["name"] != "Lamp" {
		t.Fatalf("list = %v", list)
	}

	a.Do(http.MethodDelete, devices+"/"+id, h.admin.Token, "").Want(t, http.StatusNoContent, "")
	a.Do(http.MethodDelete, devices+"/"+id, h.admin.Token, "").Want(t, http.StatusNotFound, "device_not_found")
}

func TestPermissions(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	id := claim(t, a, h)
	devices := "/v1/homes/" + h.home + "/devices"

	a.Do(http.MethodPost, devices, h.member.Token, claimBody).Want(t, http.StatusForbidden, "forbidden")
	a.Do(http.MethodPatch, devices+"/"+id, h.member.Token, `{"name":"x"}`).Want(t, http.StatusForbidden, "forbidden")
	a.Do(http.MethodDelete, devices+"/"+id, h.member.Token, "").Want(t, http.StatusForbidden, "forbidden")

	a.Do(http.MethodGet, devices, h.outsider.Token, "").Want(t, http.StatusNotFound, "home_not_found")
	a.Do(http.MethodPost, devices, h.outsider.Token, claimBody).Want(t, http.StatusNotFound, "home_not_found")
	a.Do(http.MethodPatch, devices+"/"+id, h.outsider.Token, `{"name":"x"}`).Want(t, http.StatusNotFound, "home_not_found")
	a.Do(http.MethodDelete, devices+"/"+id, h.outsider.Token, "").Want(t, http.StatusNotFound, "home_not_found")

	a.Do(http.MethodGet, devices, "", "").Want(t, http.StatusUnauthorized, "unauthorized")
}

func TestDeviceBelongsToOneHome(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	id := claim(t, a, h)
	other := a.SignIn()
	otherHome := a.CreateHome(other)

	r := a.Do(http.MethodPost, "/v1/homes/"+otherHome+"/devices", other.Token, claimBody)
	r.Want(t, http.StatusConflict, "device_claimed")
	if strings.Contains(r.Body.String(), h.home) {
		t.Fatal("the response must not reveal the other home")
	}
	a.Do(http.MethodPatch, "/v1/homes/"+otherHome+"/devices/"+id, other.Token, `{"name":"x"}`).Want(t, http.StatusNotFound, "device_not_found")
	a.Do(http.MethodDelete, "/v1/homes/"+otherHome+"/devices/"+id, other.Token, "").Want(t, http.StatusNotFound, "device_not_found")
}

func TestValidation(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	id := claim(t, a, h)
	other := a.SignIn()
	otherHome := a.CreateHome(other)
	zones, _ := a.Do(http.MethodGet, "/v1/homes/"+otherHome, other.Token, "").JSON(t)["zones"].([]any)
	zone, _ := zones[0].(map[string]any)
	foreignZone, _ := zone["id"].(string)
	devices := "/v1/homes/" + h.home + "/devices"

	for _, body := range []string{
		`{"provider":"hue","external_ref":"a","kind":"light"}`,
		`{"provider":"tuya","external_ref":"a","kind":"lock"}`,
		`{"provider":"tuya","external_ref":"","kind":"light"}`,
		`{"provider":"tuya","external_ref":"` + strings.Repeat("a", 129) + `","kind":"light"}`,
		`{"provider":"tuya","external_ref":"a","kind":"light","name":"  "}`,
		`{"provider":"tuya","external_ref":"a","kind":"light","zone_id":"` + foreignZone + `"}`,
		`{"provider":"tuya","external_ref":"a","kind":"light","zone_id":"` + uuid.NewString() + `"}`,
	} {
		a.Do(http.MethodPost, devices, h.owner.Token, body).Want(t, http.StatusBadRequest, "invalid_device")
	}
	for _, body := range []string{`{}`, `{"name":" "}`, `{"zone_id":"` + foreignZone + `"}`} {
		a.Do(http.MethodPatch, devices+"/"+id, h.owner.Token, body).Want(t, http.StatusBadRequest, "invalid_device")
	}
}

func TestDeviceLimitHoldsUnderParallelRequests(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	devices := "/v1/homes/" + h.home + "/devices"
	body := func(i int) string {
		return fmt.Sprintf(`{"provider":"tuya","external_ref":"bulb-%d","kind":"light"}`, i)
	}
	for i := range 95 {
		a.Do(http.MethodPost, devices, h.owner.Token, body(i)).Want(t, http.StatusCreated, "")
	}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			a.Do(http.MethodPost, devices, h.owner.Token, body(100+i))
		})
	}
	wg.Wait()
	if rows := a.Count(`SELECT count(*) FROM javis.devices`); rows != 100 {
		t.Fatalf("devices = %d, want 100", rows)
	}
	a.Do(http.MethodPost, devices, h.owner.Token, body(200)).Want(t, http.StatusConflict, "device_limit_reached")
}

func TestHomeWithDevicesCannotBeDeleted(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	id := claim(t, a, h)
	home := "/v1/homes/" + h.home

	a.Do(http.MethodDelete, home, h.owner.Token, "").Want(t, http.StatusConflict, "home_has_devices")
	a.Do(http.MethodGet, home, h.owner.Token, "").Want(t, http.StatusOK, "")

	a.Do(http.MethodDelete, home+"/devices/"+id, h.owner.Token, "").Want(t, http.StatusNoContent, "")
	a.Do(http.MethodDelete, home, h.owner.Token, "").Want(t, http.StatusNoContent, "")
	if scenes := a.Count(`SELECT count(*) FROM javis.scenes`); scenes != 0 {
		t.Fatalf("scenes = %d", scenes)
	}
}

func TestHomeReportsCallersUnlockRight(t *testing.T) {
	a := apitest.New(t)
	h := setUp(a)
	if unlock := a.Do(http.MethodGet, "/v1/homes/"+h.home, h.owner.Token, "").JSON(t)["can_unlock"]; unlock != true {
		t.Fatalf("owner can_unlock = %v", unlock)
	}
	if unlock := a.Do(http.MethodGet, "/v1/homes/"+h.home, h.member.Token, "").JSON(t)["can_unlock"]; unlock != false {
		t.Fatalf("member can_unlock = %v", unlock)
	}
}
