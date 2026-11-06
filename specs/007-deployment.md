# Deploy the API for a personal release build

Status: In progress (local stack verified; nothing is on the server yet)  
Updated: 2026-11-05

## Context

The owner wants a release build of the app on their own iPhone. A phone cannot
reach `127.0.0.1`, so the API needs a public HTTPS address. The available server
also runs customer work, so the deployment must not disturb it.

The server was inspected read-only on 2026-11-05. Its specifics stay out of this
public repository. What matters here: ports 80 and 443 belong to one shared Caddy on
one Docker network, and there is enough memory and disk for this small stack.

## Goal

Run the API and its own PostgreSQL on that server at a new HTTPS subdomain, without
changing any customer container, volume, or existing Caddy site.

## Non-goals

- Serving real customers. OTP debug mode stays on until an SMS provider exists.
- CI/CD, staging, blue-green rollout, or App Store distribution.
- Reclaiming the disk space, which is a separate decision for the owner.

## Contract

- `https://<api-subdomain>` serves the API unchanged. `/health/live` and
  `/health/ready` keep their contracts from `001-api-bootstrap.md`.
- The app is built with `--dart-define=JAVIS_API_URL=https://<api-subdomain>`.
- Requests reach `javis-api:8080` only through Caddy. No host port is published.

## Acceptance criteria

- `deploy/compose.yaml` publishes no host port, keeps the database on an internal
  network, and caps CPU, memory, and log size for every long-running service.
- Images contain no `.env` file and the API runs as a non-root user.
- Migrations run only on request, never at API startup.
- Every customer site answers as it did before the change.
- A release build on the owner's iPhone can sign in against the deployed API.

## Implementation notes

- `Dockerfile` has two targets: `api` (the default) and `migrate` (goose plus the
  SQL files). It cross-compiles, so a Mac can build `linux/amd64` without emulation.
- Images are built on the Mac and copied over, so nothing compiles on the shared
  server and no new build cache lands on its disk. Each image is under 30 MB.
- The stack has its own compose project (`name: javis`), so its containers, volume,
  and `data` network cannot collide with any other project.
- `edge` joins the shared Caddy's existing network as an external network. Compose cannot
  create or remove an external network.
- `APP_ENV=development` is required because `internal/config` accepts
  `OTP_SENDER=debug` only in that mode.
- `DATABASE_URL` is assembled in compose, so `POSTGRES_PASSWORD` must be URL-safe.
  Generate it with `openssl rand -hex 24`.

### Files on the server

`compose.yaml` and `.env` (root only, mode 600) in one directory on the server:

```text
JAVIS_IMAGE_TAG=<git-sha>
JAVIS_EDGE_NETWORK=<caddy-network>
POSTGRES_PASSWORD=<REDACTED>
OTP_PEPPER=<REDACTED>
```

### Caddy block

Append to the shared Caddyfile, leaving every existing block as it is. TLS details
must match the existing sites; read them before finalizing this block.

```text
<api-subdomain> {
    reverse_proxy javis-api:8080
}
```

### Rollout

Each server step needs the owner's approval, and steps that need root are run by
the owner.

1. Add a DNS record for `<api-subdomain>` pointing at the server.
2. Build both images for `linux/amd64`, tagged with the commit SHA.
3. Copy the images and `deploy/compose.yaml` to the server, then `docker load`.
4. Create the stack's `.env`, then `docker compose up -d db`.
5. `docker compose run --rm migrate up`, then `docker compose up -d api`.
6. Check `http://javis-api:8080/health/ready` from a container on the Caddy network.
7. Back up the Caddyfile, append the block, run `caddy validate`, then `caddy reload`.
8. Check `https://<api-subdomain>/health/ready` and every customer site.
9. Build the release app with `JAVIS_API_URL`, then install it on the iPhone.

### Rollback

- API stack: `docker compose down` in the stack directory. This keeps the JAVIS volume and
  touches nothing outside the project.
- Caddy: restore the backup and run `caddy reload`.

## Risks

- **Debug OTP on a public URL.** Anyone who finds the address can request a code for
  any number and sign in, because the API returns the code. The owner accepted this
  for personal use on 2026-11-05. Keep real household data out and add SMS before
  anyone else uses the app.
- **Shared Caddy.** A bad config could affect customer sites. Mitigated by validating
  before reload, keeping a backup, and appending only.
- **Shared network.** On the shared network the API can reach customer containers. They
  remain protected by their own credentials, and the debug-mode API holds none.
- **Shared disk.** The JAVIS database grows on that same disk.
- **Backups.** No database backup exists yet. Data is disposable until SMS ships.

## Verification

Run on the Mac on 2026-11-05 with throwaway secrets and a throwaway edge network,
using the `linux/amd64` images under emulation:

- Both images build; the API image is 20.2 MB and the migrate image 26.7 MB.
- `compose config` passes, and all six migrations apply through `migrate up`.
- The API reports healthy; `/health/ready` returns 200 from the edge network.
- `POST /v1/auth/otp/request` returns 202 from the edge network.
- `db` does not resolve from the edge network, and no host port is published.
- The API runs as uid 10001, the image contains no `.env*` file, and limits apply
  (256 MiB, 0.5 CPU, 10 MB logs × 3).
- The API logs contain no password, pepper, or connection string.

Not verified: `linux/amd64` on real hardware, the stack on the server, Caddy and TLS,
and the release app. The shared Caddy's existing TLS setup and the DNS provider of
the chosen domain have not been read.
