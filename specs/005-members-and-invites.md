# Home members and invitations

Status: Proposed — depends on `004-homes-and-zones.md`  
Updated: 2026-11-05

## Context

The app has the approved UI on mock data: Invite accept (08), Home settings
member list (22), Members invite (23). A member is invited by mobile number and
sees the invitation on signing in with that number. Roles shown today:

- Owner: everything.
- Admin: add devices, edit zones and scenes, invite others.
- Member: use lights and scenes, see door status.

Unlocking the door is a separate per-member right "set by the home owner"
(`memberRightsNote`). This right controls physical access, so it is the most
sensitive value in this spec.

## Goal

Invite people to a home, let them accept or decline, and let the owner manage
roles, unlock rights and removal.

## Non-goals

- Sending an SMS or push for invitations (decided 2026-11-05: no SMS).
- Transferring ownership.
- Enforcing `can_unlock` on the lock itself (device/lock spec). This spec stores it.

## User flow

1. Owner or admin opens Members invite, enters a number, role and unlock right
   → `POST …/invites`.
2. The invitee signs in; `GET /v1/me/invites` returns pending invitations; the
   onboarding card or Invite accept screen shows them.
3. Accept joins the home; decline removes the invitation from the list.
4. The owner changes a role or unlock right, or removes someone.

## API contract

All routes require a session. Non-members get 404 `home_not_found`.

| Operation | Who | Success | Errors |
| --- | --- | --- | --- |
| `GET /v1/homes/{homeId}/members` | any member | 200 `{members[], invites[]}` | 404 |
| `POST /v1/homes/{homeId}/invites` `{phone, role, can_unlock}` | owner, admin | 201 `Invite` | 400 `invalid_phone`, 403 `forbidden`, 409 `already_member`, 409 `already_invited`, 409 `member_limit_reached` |
| `DELETE /v1/homes/{homeId}/invites/{inviteId}` | owner, admin | 204 | 403, 404 `invite_not_found` |
| `PATCH /v1/homes/{homeId}/members/{userId}` `{role?, can_unlock?}` | owner | 200 `Member` | 400, 403, 404 `member_not_found` |
| `DELETE /v1/homes/{homeId}/members/{userId}` | see below | 204 | 403, 404 |
| `GET /v1/me/invites` | invitee | 200 `{items: ReceivedInvite[]}` | — |
| `POST /v1/me/invites/{inviteId}/accept` | invitee | 200 `Home` | 404 `invite_not_found` |
| `POST /v1/me/invites/{inviteId}/decline` | invitee | 204 | 404 |

```text
Member          {user_id, name, role, can_unlock}
Invite          {id, phone_masked, role, can_unlock, expires_at}
ReceivedInvite  {id, home_name, invited_by_name, role, zone_count, expires_at}
```

`phone_masked` follows the app's `089 xxx 4521` form. Joined members' phone
numbers are never returned: the app does not display them, so they stay private
between household members. An expired or someone else's invitation is
indistinguishable from a missing one (404).

Invitations expire 7 days after creation. Limit: 20 members plus pending
invitations per home.

## Permission rules (confirmed by the owner 2026-11-05)

| Action | Owner | Admin | Member |
| --- | --- | --- | --- |
| Invite as member, `can_unlock=false` | yes | yes | no |
| Invite as admin | yes | no | no |
| Grant `can_unlock=true` (invite or edit) | yes | no | no |
| Change role or unlock right | yes | no | no |
| Remove a member | yes | yes | no |
| Remove an admin | yes | no | no |
| Remove the owner | no | no | no |
| Leave a home (remove self) | no | yes | yes |

Without these rules an admin could promote a friend or grant door access, which
contradicts "set by the home owner". The current Members screen shows the admin
role and unlock switch to admins too, so the app must hide them for admins.

## Data model

```text
javis.home_invites  id uuid PK, home_id FK ON DELETE CASCADE, phone text, role text,
                    can_unlock bool, invited_by FK users, status text, expires_at,
                    created_at, responded_at NULL
                    CHECK role IN ('admin','member')
                    CHECK status IN ('pending','accepted','declined','revoked')
                    UNIQUE (home_id, phone) WHERE status = 'pending'
                    INDEX (phone) WHERE status = 'pending'
```

`home_members` comes from `004-homes-and-zones.md`.

## Implementation notes

- Stays in module `internal/home`, split into `members.go` / `invites.go`.
- Accept runs in one `WithTx`: lock the invitation row, re-check pending and not
  expired, confirm the session's phone equals `invites.phone`, insert the member,
  set `accepted`. Two concurrent accepts create one membership.
- The phone match is the only proof of identity, and OTP verified it. An
  invitation for a number never grants access to anyone else.
- Removing a member deletes the row, so `can_unlock` is revoked at once. Revoking
  sessions is not needed because access is checked per request.
- Accepting an invitation when already a member marks it `accepted` without
  changing the existing role.

## Acceptance criteria

- Every row of the permission table has a test, including denied cases.
- Accept works only for the invited number; a different signed-in user gets 404.
- Expired, revoked, declined and already-used invitations cannot be accepted.
- Duplicate pending invitations return 409; a declined number can be re-invited.
- Concurrent accepts produce one membership.
- Removed members lose access on their next request.
- Responses never include a joined member's phone number or a full pending number.

## Risks

- A recycled mobile number could inherit a pending invitation; the 7-day expiry
  limits that window.
- An invitation to a stranger's number reveals the home's name and inviter's
  name to that person, which is why it needs the inviter's care. Unlock rights
  are only effective after the lock spec.

## Decisions

- **D2 Permission table** above: confirmed by the owner on 2026-11-05.
- **D3 Invitation delivery**: no SMS, confirmed by the owner on 2026-11-05. This
  matches the app text "they see the invitation as soon as they sign in"; the
  invitee hears about it from the inviter.

## Verification

Not implemented.

## Current status

Spec only.
