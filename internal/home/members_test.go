package home_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

const someNumber = "+66891234521"

func (a *api) invite(homeID string, by person, phone, role string, canUnlock bool) response {
	a.t.Helper()
	body := fmt.Sprintf(`{"phone":%q,"role":%q,"can_unlock":%t}`, phone, role, canUnlock)
	return a.do(http.MethodPost, "/v1/homes/"+homeID+"/invites", by.token, body)
}

func (a *api) mustInvite(homeID string, by person, phone, role string, canUnlock bool) string {
	a.t.Helper()
	r := a.invite(homeID, by, phone, role, canUnlock)
	if r.Code != http.StatusCreated {
		a.t.Fatalf("invite: HTTP %d %s", r.Code, r.Body)
	}
	id, _ := r.json(a.t)["id"].(string)
	return id
}

func (a *api) answer(inviteID string, who person, verb string) response {
	return a.do(http.MethodPost, "/v1/me/invites/"+inviteID+"/"+verb, who.token, "")
}

func (a *api) canSeeHome(homeID string, who person) bool {
	return a.do(http.MethodGet, "/v1/homes/"+homeID, who.token, "").Code == http.StatusOK
}

// household is a home with an owner, an admin, a member and an outsider.
type household struct {
	home                           string
	owner, admin, member, outsider person
}

func (a *api) household() household {
	a.t.Helper()
	h := household{owner: a.signIn(), admin: a.signIn(), member: a.signIn(), outsider: a.signIn()}
	h.home = a.createHome(h.owner)
	a.addMember(h.home, h.admin, "admin")
	a.addMember(h.home, h.member, "member")
	return h
}

func wantStatus(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("HTTP %d %s, want %d", r.Code, r.Body, status)
	}
	if code != "" && r.errorCode(t) != code {
		t.Fatalf("error = %s, want %s", r.Body, code)
	}
}

func TestInvitePermissions(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		canUnlock bool
		inviter   func(household) person
		status    int
		code      string
	}{
		{"owner invites member", "member", false, func(h household) person { return h.owner }, http.StatusCreated, ""},
		{"owner invites member who can unlock", "member", true, func(h household) person { return h.owner }, http.StatusCreated, ""},
		{"owner invites admin", "admin", false, func(h household) person { return h.owner }, http.StatusCreated, ""},
		{"admin invites member", "member", false, func(h household) person { return h.admin }, http.StatusCreated, ""},
		{"admin cannot invite admin", "admin", false, func(h household) person { return h.admin }, http.StatusForbidden, "forbidden"},
		{"admin cannot grant unlock", "member", true, func(h household) person { return h.admin }, http.StatusForbidden, "forbidden"},
		{"member cannot invite", "member", false, func(h household) person { return h.member }, http.StatusForbidden, "forbidden"},
		{"outsider sees no home", "member", false, func(h household) person { return h.outsider }, http.StatusNotFound, "home_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAPI(t)
			h := a.household()
			wantStatus(t, a.invite(h.home, tc.inviter(h), someNumber, tc.role, tc.canUnlock), tc.status, tc.code)
		})
	}
}

func TestUpdateMemberIsOwnerOnly(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	path := "/v1/homes/" + h.home + "/members/" + h.member.id

	wantStatus(t, a.do(http.MethodPatch, path, h.admin.token, `{"can_unlock":true}`), http.StatusForbidden, "forbidden")
	wantStatus(t, a.do(http.MethodPatch, path, h.member.token, `{"can_unlock":true}`), http.StatusForbidden, "forbidden")
	wantStatus(t, a.do(http.MethodPatch, path, h.outsider.token, `{"can_unlock":true}`), http.StatusNotFound, "home_not_found")

	updated := a.do(http.MethodPatch, path, h.owner.token, `{"can_unlock":true}`)
	wantStatus(t, updated, http.StatusOK, "")
	if body := updated.json(t); body["can_unlock"] != true || body["role"] != "member" {
		t.Fatalf("updated = %v", body)
	}
	promoted := a.do(http.MethodPatch, path, h.owner.token, `{"role":"admin"}`).json(t)
	if promoted["role"] != "admin" || promoted["can_unlock"] != true {
		t.Fatalf("promoted = %v", promoted)
	}

	wantStatus(t, a.do(http.MethodPatch, path, h.owner.token, `{}`), http.StatusBadRequest, "invalid_member_update")
	wantStatus(t, a.do(http.MethodPatch, path, h.owner.token, `{"role":"owner"}`), http.StatusBadRequest, "invalid_member_update")
	wantStatus(t, a.do(http.MethodPatch, path, h.owner.token, `{"role":"root"}`), http.StatusBadRequest, "invalid_member_update")
	ownerPath := "/v1/homes/" + h.home + "/members/" + h.owner.id
	wantStatus(t, a.do(http.MethodPatch, ownerPath, h.owner.token, `{"role":"member"}`), http.StatusForbidden, "forbidden")
	missing := "/v1/homes/" + h.home + "/members/" + uuid.NewString()
	wantStatus(t, a.do(http.MethodPatch, missing, h.owner.token, `{"role":"member"}`), http.StatusNotFound, "member_not_found")
}

func TestRemovePermissions(t *testing.T) {
	cases := []struct {
		name   string
		actor  func(household) person
		target func(household) person
		status int
	}{
		{"owner removes member", func(h household) person { return h.owner }, func(h household) person { return h.member }, http.StatusNoContent},
		{"owner removes admin", func(h household) person { return h.owner }, func(h household) person { return h.admin }, http.StatusNoContent},
		{"admin removes member", func(h household) person { return h.admin }, func(h household) person { return h.member }, http.StatusNoContent},
		{"admin leaves", func(h household) person { return h.admin }, func(h household) person { return h.admin }, http.StatusNoContent},
		{"member leaves", func(h household) person { return h.member }, func(h household) person { return h.member }, http.StatusNoContent},
		{"owner cannot leave", func(h household) person { return h.owner }, func(h household) person { return h.owner }, http.StatusForbidden},
		{"admin cannot remove owner", func(h household) person { return h.admin }, func(h household) person { return h.owner }, http.StatusForbidden},
		{"member cannot remove admin", func(h household) person { return h.member }, func(h household) person { return h.admin }, http.StatusForbidden},
		{"outsider sees no home", func(h household) person { return h.outsider }, func(h household) person { return h.member }, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAPI(t)
			h := a.household()
			target := tc.target(h)
			r := a.do(http.MethodDelete, "/v1/homes/"+h.home+"/members/"+target.id, tc.actor(h).token, "")
			wantStatus(t, r, tc.status, "")
			if gone := !a.canSeeHome(h.home, target); gone != (tc.status == http.StatusNoContent) {
				t.Fatalf("target gone = %v after HTTP %d", gone, tc.status)
			}
		})
	}
}

func TestAdminCannotRemoveAnotherAdmin(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	second := a.signIn()
	a.addMember(h.home, second, "admin")
	r := a.do(http.MethodDelete, "/v1/homes/"+h.home+"/members/"+second.id, h.admin.token, "")
	wantStatus(t, r, http.StatusForbidden, "forbidden")
}

func TestMemberCannotRemoveAnotherMember(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	second := a.signIn()
	a.addMember(h.home, second, "member")
	r := a.do(http.MethodDelete, "/v1/homes/"+h.home+"/members/"+second.id, h.member.token, "")
	wantStatus(t, r, http.StatusForbidden, "forbidden")
	if !a.canSeeHome(h.home, second) {
		t.Fatal("the other member must stay in the home")
	}
}

func TestRemovingUnknownPerson(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	r := a.do(http.MethodDelete, "/v1/homes/"+h.home+"/members/"+uuid.NewString(), h.owner.token, "")
	wantStatus(t, r, http.StatusNotFound, "member_not_found")
}

func TestRemovedMemberLosesAccessOnNextRequest(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	a.exec(`UPDATE javis.home_members SET can_unlock = true WHERE user_id = $1`, h.member.id)
	a.do(http.MethodDelete, "/v1/homes/"+h.home+"/members/"+h.member.id, h.owner.token, "")
	wantStatus(t, a.do(http.MethodGet, "/v1/homes/"+h.home, h.member.token, ""), http.StatusNotFound, "home_not_found")
	if rows := a.count(`SELECT count(*) FROM javis.home_members WHERE user_id = $1`, h.member.id); rows != 0 {
		t.Fatal("the unlock right must go with the membership")
	}
}

func TestInviteFlow(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	guest := a.signIn()
	a.exec(`UPDATE javis.users SET name = 'แม่' WHERE id = $1`, h.owner.id)
	inviteID := a.mustInvite(h.home, h.owner, guest.phone, "member", true)

	received := a.do(http.MethodGet, "/v1/me/invites", guest.token, "").json(t)
	items, _ := received["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("received = %v", received)
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != inviteID || item["home_name"] != "บ้านทดสอบ" || item["invited_by_name"] != "แม่" || item["role"] != "member" || item["zone_count"] != float64(1) {
		t.Fatalf("item = %v", item)
	}

	accepted := a.answer(inviteID, guest, "accept")
	wantStatus(t, accepted, http.StatusOK, "")
	if body := accepted.json(t); body["id"] != h.home || body["role"] != "member" {
		t.Fatalf("accepted = %v", body)
	}
	if !a.canSeeHome(h.home, guest) {
		t.Fatal("the guest must now be in the home")
	}
	if rows := a.count(`SELECT count(*) FROM javis.home_members WHERE user_id = $1 AND can_unlock`, guest.id); rows != 1 {
		t.Fatal("the unlock right granted by the owner must carry over")
	}
	r := a.do(http.MethodGet, "/v1/me/invites", guest.token, "")
	if strings.TrimSpace(r.Body.String()) != `{"items":[]}` {
		t.Fatalf("after accepting: %s", r.Body)
	}
}

func TestInviteAsksForAnInvitedNumberOnly(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	guest := a.signIn()
	inviteID := a.mustInvite(h.home, h.owner, guest.phone, "member", false)
	for _, verb := range []string{"accept", "decline"} {
		wantStatus(t, a.answer(inviteID, h.outsider, verb), http.StatusNotFound, "invite_not_found")
	}
	if a.canSeeHome(h.home, h.outsider) {
		t.Fatal("an invitation must not grant access to another number")
	}
	wantStatus(t, a.answer(inviteID, guest, "accept"), http.StatusOK, "")
}

func TestClosedInvitesCannotBeAccepted(t *testing.T) {
	closers := map[string]func(a *api, h household, inviteID string, guest person){
		"expired": func(a *api, _ household, inviteID string, _ person) {
			a.exec(`UPDATE javis.home_invites SET expires_at = now() - interval '1 second' WHERE id = $1`, inviteID)
		},
		"revoked": func(a *api, h household, inviteID string, _ person) {
			wantStatus(a.t, a.do(http.MethodDelete, "/v1/homes/"+h.home+"/invites/"+inviteID, h.admin.token, ""), http.StatusNoContent, "")
		},
		"declined": func(a *api, _ household, inviteID string, guest person) {
			wantStatus(a.t, a.answer(inviteID, guest, "decline"), http.StatusNoContent, "")
		},
		"already used": func(a *api, _ household, inviteID string, guest person) {
			wantStatus(a.t, a.answer(inviteID, guest, "accept"), http.StatusOK, "")
			a.exec(`DELETE FROM javis.home_members WHERE user_id = $1`, guest.id)
		},
	}
	for name, closeInvite := range closers {
		t.Run(name, func(t *testing.T) {
			a := newAPI(t)
			h := a.household()
			guest := a.signIn()
			inviteID := a.mustInvite(h.home, h.owner, guest.phone, "member", false)
			closeInvite(a, h, inviteID, guest)
			wantStatus(t, a.answer(inviteID, guest, "accept"), http.StatusNotFound, "invite_not_found")
			wantStatus(t, a.answer(inviteID, guest, "decline"), http.StatusNotFound, "invite_not_found")
			if a.canSeeHome(h.home, guest) {
				t.Fatal("a closed invitation must not grant access")
			}
		})
	}
}

func TestInviteConflicts(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	guest := a.signIn()

	wantStatus(t, a.invite(h.home, h.owner, h.member.phone, "member", false), http.StatusConflict, "already_member")

	first := a.mustInvite(h.home, h.owner, guest.phone, "member", false)
	wantStatus(t, a.invite(h.home, h.admin, guest.phone, "member", false), http.StatusConflict, "already_invited")

	wantStatus(t, a.answer(first, guest, "decline"), http.StatusNoContent, "")
	second := a.mustInvite(h.home, h.owner, guest.phone, "member", false)

	a.exec(`UPDATE javis.home_invites SET expires_at = now() - interval '1 second' WHERE id = $1`, second)
	third := a.mustInvite(h.home, h.owner, guest.phone, "member", false)
	if status := a.count(`SELECT count(*) FROM javis.home_invites WHERE id = $1 AND status = 'expired'`, second); status != 1 {
		t.Fatal("the lapsed invitation must be marked expired")
	}

	wantStatus(t, a.answer(third, guest, "accept"), http.StatusOK, "")
}

func TestInviteValidation(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	for _, phone := range []string{"", "0891234521", "+15551234567", "+66212345678", "+6689123452"} {
		wantStatus(t, a.invite(h.home, h.owner, phone, "member", false), http.StatusBadRequest, "invalid_phone")
	}
	for _, role := range []string{"owner", "root", ""} {
		wantStatus(t, a.invite(h.home, h.owner, someNumber, role, false), http.StatusBadRequest, "invalid_role")
	}
}

func TestRevokeInvite(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	inviteID := a.mustInvite(h.home, h.owner, someNumber, "member", false)
	path := "/v1/homes/" + h.home + "/invites/" + inviteID

	wantStatus(t, a.do(http.MethodDelete, path, h.member.token, ""), http.StatusForbidden, "forbidden")
	wantStatus(t, a.do(http.MethodDelete, path, h.outsider.token, ""), http.StatusNotFound, "home_not_found")

	otherOwner := a.signIn()
	otherHome := a.createHome(otherOwner)
	wantStatus(t, a.do(http.MethodDelete, "/v1/homes/"+otherHome+"/invites/"+inviteID, otherOwner.token, ""), http.StatusNotFound, "invite_not_found")

	wantStatus(t, a.do(http.MethodDelete, path, h.owner.token, ""), http.StatusNoContent, "")
	wantStatus(t, a.do(http.MethodDelete, path, h.owner.token, ""), http.StatusNotFound, "invite_not_found")
}

func TestRosterKeepsNumbersPrivate(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	a.mustInvite(h.home, h.owner, someNumber, "admin", false)

	r := a.do(http.MethodGet, "/v1/homes/"+h.home+"/members", h.member.token, "")
	wantStatus(t, r, http.StatusOK, "")
	raw := r.Body.String()
	for _, secret := range []string{someNumber, "0891234521", h.owner.phone, h.admin.phone, h.member.phone, "\"phone\""} {
		if strings.Contains(raw, secret) {
			t.Fatalf("roster exposes %q: %s", secret, raw)
		}
	}
	body := r.json(t)
	members, _ := body["members"].([]any)
	invites, _ := body["invites"].([]any)
	first, _ := members[0].(map[string]any)
	invite, _ := invites[0].(map[string]any)
	if len(members) != 3 || first["role"] != "owner" || first["can_unlock"] != true || invite["phone_masked"] != "089 xxx 4521" {
		t.Fatalf("roster = %v", body)
	}
	wantStatus(t, a.do(http.MethodGet, "/v1/homes/"+h.home+"/members", h.outsider.token, ""), http.StatusNotFound, "home_not_found")
}

func TestExistingMemberKeepsRoleWhenAccepting(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	inviteID := uuid.NewString()
	a.exec(`INSERT INTO javis.home_invites (id, home_id, phone, role, can_unlock, invited_by, expires_at)
		VALUES ($1, $2, $3, 'member', false, $4, now() + interval '1 day')`, inviteID, h.home, h.admin.phone, h.owner.id)
	accepted := a.answer(inviteID, h.admin, "accept")
	wantStatus(t, accepted, http.StatusOK, "")
	if role := accepted.json(t)["role"]; role != "admin" {
		t.Fatalf("role = %v", role)
	}
}

func TestSeatLimitCountsOpenInvites(t *testing.T) {
	a := newAPI(t)
	h := a.household() // Three seats taken.
	for i := range 17 {
		a.mustInvite(h.home, h.owner, fmt.Sprintf("+6682000%04d", i), "member", false)
	}
	wantStatus(t, a.invite(h.home, h.owner, someNumber, "member", false), http.StatusConflict, "member_limit_reached")

	a.exec(`UPDATE javis.home_invites SET expires_at = now() - interval '1 second'
		WHERE id = (SELECT id FROM javis.home_invites ORDER BY created_at LIMIT 1)`)
	a.mustInvite(h.home, h.owner, someNumber, "member", false)
}

func TestSeatLimitHoldsUnderParallelRequests(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	for i := range 15 {
		a.mustInvite(h.home, h.owner, fmt.Sprintf("+6682000%04d", i), "member", false)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			a.invite(h.home, h.owner, fmt.Sprintf("+6683000%04d", i), "member", false)
		})
	}
	wg.Wait()
	if open := a.count(`SELECT count(*) FROM javis.home_invites WHERE status = 'pending'`); open != 17 {
		t.Fatalf("open invitations = %d, want 17", open)
	}
}

func TestConcurrentAcceptsCreateOneMembership(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	guest := a.signIn()
	inviteID := a.mustInvite(h.home, h.owner, guest.phone, "member", false)

	statuses := make([]int, 8)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			statuses[i] = a.answer(inviteID, guest, "accept").Code
		})
	}
	wg.Wait()

	succeeded := 0
	for _, status := range statuses {
		switch status {
		case http.StatusOK:
			succeeded++
		case http.StatusNotFound:
		default:
			t.Fatalf("statuses = %v", statuses)
		}
	}
	if succeeded != 1 {
		t.Fatalf("statuses = %v", statuses)
	}
	if rows := a.count(`SELECT count(*) FROM javis.home_members WHERE user_id = $1`, guest.id); rows != 1 {
		t.Fatalf("memberships = %d", rows)
	}
}

func TestDeletingHomeRemovesItsInvites(t *testing.T) {
	a := newAPI(t)
	h := a.household()
	guest := a.signIn()
	inviteID := a.mustInvite(h.home, h.owner, guest.phone, "member", false)
	wantStatus(t, a.do(http.MethodDelete, "/v1/homes/"+h.home, h.owner.token, ""), http.StatusNoContent, "")
	if rows := a.count(`SELECT count(*) FROM javis.home_invites`); rows != 0 {
		t.Fatalf("invites = %d", rows)
	}
	wantStatus(t, a.answer(inviteID, guest, "accept"), http.StatusNotFound, "invite_not_found")
}

func TestMemberRoutesRequireASession(t *testing.T) {
	a := newAPI(t)
	id := uuid.NewString()
	for _, route := range [][2]string{
		{http.MethodGet, "/v1/homes/" + id + "/members"},
		{http.MethodPatch, "/v1/homes/" + id + "/members/" + id},
		{http.MethodDelete, "/v1/homes/" + id + "/members/" + id},
		{http.MethodPost, "/v1/homes/" + id + "/invites"},
		{http.MethodDelete, "/v1/homes/" + id + "/invites/" + id},
		{http.MethodGet, "/v1/me/invites"},
		{http.MethodPost, "/v1/me/invites/" + id + "/accept"},
		{http.MethodPost, "/v1/me/invites/" + id + "/decline"},
	} {
		if r := a.do(route[0], route[1], "", `{}`); r.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: HTTP %d %s", route[0], route[1], r.Code, r.Body)
		}
	}
}
