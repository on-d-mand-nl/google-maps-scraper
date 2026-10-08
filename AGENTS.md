# Development Guide for Agents

## Build/Test Commands
- `make test` - Run all unit tests with race detection
- `make test-cover` - Run tests with coverage statistics  
- `make lint` - Run golangci-lint with project configuration
- `make vet` - Run go vet static analysis
- `make format` - Format code with gofmt
- `go test ./path/to/package` - Run tests for a specific package

## Code Style Guidelines
- Use `gofmt` for formatting (spaces, not tabs)
- Import order: standard library, third-party, local packages (prefix: github.com/gosom/google-maps-scraper)
- Use descriptive variable names (e.g., `entry`, `cfg`, `ctx`)
- Error handling: return errors, use `fmt.Errorf` with wrapping (`%w`)
- Use struct tags for JSON marshaling: `json:"field_name"`
- Constants use CamelCase (e.g., `RunModeFile`)
- Interface names end with -er suffix (e.g., `Runner`, `S3Uploader`)
- Use context.Context as first parameter in functions
- Prefer early returns to reduce nesting
- Use meaningful package names that reflect their purpose
- Add godoc comments for exported types and functions
- Use `nolint` comments sparingly with explanations
- Avoid magic numbers, use named constants or comment them

## Deployment Reference (last verified 2026-10-08)

This project is deployed as the self-hosted SaaS edition: one central API/admin
server, native PostgreSQL on the central Mac, and five distributed workers.
All machines communicate over Tailscale. The user wants all workers in fast mode.

### Hosts

| Role | Tailscale hostname | Tailscale IPv4 | SSH user | Platform |
| --- | --- | --- | --- | --- |
| Central SaaS + PostgreSQL | `mac-studio-van-horecainsights` | `100.88.1.114` | `horecainsights` | macOS / ARM64 |
| Worker | `helpdesks-mac-mini` | `100.109.33.11` | `helpdeskon-d-mand` | macOS / ARM64 |
| Worker | `on-d-mand-nuc` | `100.92.229.34` | `on-d-mand` | Ubuntu / AMD64 |
| Worker | `on-d-mand-lenovo-ideapad-320-15ikb` | `100.67.227.27` | `on-d-mand` | Ubuntu / AMD64 |
| Worker | `mac-studio-m4-1` | `100.66.159.43` | `macstudiohorecainsightsm4` | macOS / ARM64 |
| Worker | `mac-studio-m4-2` | `100.115.122.26` | `m4` | macOS / ARM64 |

These addresses and runtime details are a deployment snapshot; verify before
making changes. Do not assume an SSH connection or temporary control socket from
a previous session still exists. Never put SSH passwords, database passwords,
API keys, or encryption keys in this file or in version control.

### Central server

- Deployment directory: `~/gmapssaas` on `mac-studio-van-horecainsights`.
- Dashboard: `http://100.88.1.114:8090/admin/login`.
- Workers page: `http://100.88.1.114:8090/admin/workers`.
- Compose project: `gmapssaas-central`; live configuration: `central.yaml`.
- Current server image: `gmapssaas:dashboard`, built from this checkout using
  `Dockerfile.saas`. This is a locally loaded tag, not a published registry image.
- Database: native DBngin PostgreSQL 17 on port `5433`, database and role
  `gmapssaas`. Other databases include `gmaps` and Kestra databases; do not change
  them when maintaining this deployment.
- The server container reaches PostgreSQL at `host.docker.internal:5433`.
  Remote workers use `100.88.1.114:5433` over Tailscale.
- Native PostgreSQL data directory:
  `~/Development/Databases/postgresql`; config file:
  `~/Development/Databases/postgresql.conf`.
- Dedicated `gmapssaas` host rules were added at the beginning of `pg_hba.conf`
  to require SCRAM password authentication. The native instance already listened
  on all addresses; do not assume it is bound exclusively to Tailscale.
- The SaaS database was migrated from its initial Docker PostgreSQL container.
  `gmapssaas-central-db-1` is stopped and its volume retained for rollback.
  Migration backups are under `~/gmapssaas/backups/native-migration/`.
- `.env` holds database/encryption secrets. `.env.worker` holds the worker DSN.
  Admin username is `admin`; generated credentials are stored privately in
  `~/gmapssaas/admin-credentials.txt`. Do not print or commit these files.
- Preserve `ENCRYPTION_KEY` when moving or restoring the database: it encrypts
  stored application settings.
- An initial central deployment on the development Mac was stopped when the user
  chose the remote central Mac. Do not start a second central deployment locally.

Central operations (run on the central Mac):

```sh
export PATH="/usr/local/bin:$PATH"
cd ~/gmapssaas
docker compose --env-file .env -f central.yaml ps
docker compose --env-file .env -f central.yaml logs --tail 100 server
docker compose --env-file .env -f central.yaml up -d --pull never
```

The Compose migration service uses `gmapssaas admin migrate` to apply the embedded
SaaS migrations from `migrations/`. The basic scraper's `scripts/migrations/`
contains a different schema and must not be used for the SaaS database.

### Workers and fast mode

- Each host has `~/gmapssaas/worker.yaml` and a private `.env.worker` file.
- Compose project: `gmapssaas-workers`; container:
  `gmapssaas-workers-worker-1`.
- All five live configurations use `command: [worker, --fast]`.
- Submit jobs with `fast_mode: true` (enable Fast mode in the job interface).
  Fast mode also requires geographic coordinates; use the API validation as the
  source of truth for input requirements.
- Worker fetching mode is chosen at process startup, while SaaS job mode is a
  per-job flag. There is no automatic mixed-mode routing. Browser-mode workers
  processing fast-mode jobs can fail with `playwright: Download is starting`
  because the fast data endpoint is not a navigable browser page. Conversely,
  do not submit browser-mode jobs to this all-fast-mode fleet.
- One River scrape job is processed at a time per worker process. Raising the
  `--concurrency` flag does not increase this; the current implementation fixes
  per-process concurrency to one.
- Current image tags: Mac mini `gmapssaas:local`; both M4 Macs
  `gmapssaas:dashboard`; both Ubuntu machines `gmapssaas:worker-amd64`.
  Tags are local to each Docker host. Match ARM64/AMD64 when building or loading.
- Health endpoint: `http://<worker-tailscale-ip>:18080/health`, mapped to container
  port `8080` and bound only to that host's Tailscale IP.
- Restart policy: `unless-stopped`. Docker and Tailscale must be running, and
  hosts must remain awake. On Ubuntu, Docker service startup was verified enabled.
  macOS container restart depends on Docker Desktop running.
- Browser caches were retained from browser-mode setup. Mac mini uses a bind
  mount at `~/gmapssaas/browser-cache`; other workers use a named browser-cache
  volume. Fast mode uses the stealth HTTP fetcher and does not require Chromium.
- The NUC's browser CDN download returned a regional 403 during initial setup.
  Official AMD64 Chrome/FFmpeg archives were downloaded on the development Mac
  and transferred into its cache (later reused on the Lenovo). Do not treat a
  health endpoint alone as proof that browser initialization has succeeded.

Worker operations (run on the worker host; prefix Docker commands with `sudo`
on the Ubuntu hosts because the supplied SSH user lacks Docker socket access):

```sh
# On macOS, if Docker is not in the SSH PATH:
export PATH="/usr/local/bin:$PATH"
cd ~/gmapssaas
docker compose --env-file .env.worker -f worker.yaml ps
docker compose --env-file .env.worker -f worker.yaml logs --tail 100 worker
docker compose --env-file .env.worker -f worker.yaml up -d --pull never
```

### Dashboard registration and monitoring

Joining the River queue does not automatically add a worker to the dashboard.
The Workers page reads `provisioned_resources`.

Manually installed workers are registered with:

- `provider = 'external'`, `resource_type = 'worker'`.
- `resource_id` and `name` equal to the Tailscale hostname.
- `ip_address` equal to the worker's Tailscale IPv4.
- `metadata.managed_externally = true`.
- Initially `status = 'provisioning'`; successful health checks promote it to
  `active`. Inspect `metadata.health.reachable` too: active status alone does not
  prove current reachability.

The central server calls `StartWorkerHealthChecks` every 30 seconds, independently
of River leadership. This was necessary because a scraper worker can become
River leader without having the central server's periodic jobs configured.
External workers are checked over HTTP on Tailscale port `18080`; cloud workers
retain their SSH-based checks. External workers display "Managed externally"
instead of cloud terminal/deletion controls. Health counters reset on restart;
stored scrape results remain in PostgreSQL.

### Repository changes and deployment workflow

- `deploy/saas/central-native.yaml`: template for native PostgreSQL hosting.
- `deploy/saas/central.yaml`: alternative with a Docker PostgreSQL service;
  this is not the current production database layout.
- `deploy/saas/worker.yaml`: buildable worker template, now defaulting to `--fast`.
- These templates are not exact copies of every deployed file. Live configs
  include per-host image tags, health bindings, and browser-cache mounts.
- `cmd/gmapssaas/cmdadmin/cmd_admin.go`: added the `admin migrate` command.
- `rqueue/worker_jobs.go`, `cmd/gmapssaas/cmdserve/cmd_serve.go`, and
  `admin/templates/workers.html`: external worker monitoring and dashboard support.
- Earlier build blockers were fixed in `gmaps/job.go` (current browser API calls)
  and `rqueue/rqueue.go` (place ID validator argument). Relevant test constructor
  calls were updated too.
- Changes made during deployment were left in the working tree; inspect Git
  status and preserve unrelated edits before further work.
- The development Mac did not have Go/gofmt on PATH. Go commands were run in a
  `golang:1.27.1-alpine` container. Use the version in `go.mod`/`Dockerfile.saas`
  as the source of truth.
- Build with `Dockerfile.saas`, not the basic scraper's `Dockerfile`.
  Build both `linux/arm64` and `linux/amd64` for fleet-wide code changes.
- Images have been distributed with `docker save`/`docker load` over SSH. Load
  the image before `compose up --pull never`; do not assume the tags are pullable.
- Keep secrets out of Docker build contexts and Git. Existing `.dockerignore`
  excludes environment files; `.gitignore` excludes local deployment secrets.
- Before worker restarts, check for active jobs. Preserve database volumes,
  encryption keys, and rollback configurations. Do not reset existing jobs or
  recreate infrastructure merely to update an image.

Last functional verification: after switching the fleet to fast mode, test job
`263` (`coffee` near `52.3820,4.6372`, zoom 14, radius 5000) completed with 20 results
saved. All five workers were reachable. Earlier browser-mode smoke tests also
verified Chromium launch; the Mac mini's test job `27` saved eight results.
These are historical checks, not guarantees of current runtime health.
