# Compose DB Agent

A tiny LLM-powered CLI that operates a **database service** in a Docker Compose stack.  
You type natural language (e.g., “ramp up db”, “reset (confirm: RESET myproj)”) and the agent calls safe, audited tools under the hood. It can auto-start Colima if Docker isn’t running and waits until the DB is **healthy** before saying it’s ready.

---

## Why this exists (use cases)

The database is the riskiest shared state in local dev. This agent gives you a single, natural-language entry point that consistently:

- Uses the right Compose flags (`-p`, `-f`, `--project-directory`) and loads your app’s `.env`.
- **Auto-starts Colima** if Docker isn’t up and **waits for the DB healthcheck**, reducing “backend can’t connect to DB” races.
- Makes destructive operations **safe** (explicit confirmation, optional interactive volume wipe, prod guard).
- Stays **project-agnostic**: change the env vars `PROJECT`, `COMPOSE_FILE`, `APP_DIR`, `APP_ENV_FILE` to target another app; it still only touches the DB service + its volume.
- Keeps operations **auditable & repeatable** via versioned tools (`DRY_RUN` supported), not ad-hoc shell.

**Great for:** onboarding, switching between projects, reliable local resets before demos/QA, teams who want safer and consistent DB workflows.  
**Probably overkill if:** you’re solo on one project and happy with a couple of Make targets.

---

## Features

- **Ramp up** only the DB service (`up -d db`) and **wait for health**
- **Ramp down** only the DB service (stop + rm)  
  *Optionally delete just the DB’s named volume*
- **Reset** the DB safely (stop & remove DB → delete **only** the DB volume → `up -d db` → wait healthy) with a confirmation phrase
- **Status** (container health) and **Logs** (tail)
- **Safety rails:** project/path validation, destructive-action confirmation, optional interactive wipe prompt, refuse when `ENV=production`

---

## Requirements

- Go 1.21+
- Docker (Docker Desktop or **Colima**)
- A Compose file with a **DB service** (default name `db`) that has a **healthcheck**

Example healthcheck:

```yaml
services:
  db:
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER:-postgres} -d ${POSTGRES_DB:-postgres}"]
      interval: 5s
      timeout: 3s
      retries: 20
      start_period: 10s
```

---

## Configuration

Create a .env in this repo (don’t commit secrets).
Put comments on their own lines (the loader doesn’t strip inline #).

```bash
# --- Required ---
ANTHROPIC_API_KEY=sk-ant-...

# Targeting (project-agnostic)
PROJECT=myproj
DB_SERVICE=db
DB_VOLUME=db_data            # volume key from compose; actual name becomes <PROJECT>_<DB_VOLUME>

# Where the app lives (paths can be relative to this agent repo)
APP_DIR=../myapp
COMPOSE_FILE=../myapp/docker-compose.yml
APP_ENV_FILE=../myapp/.env   # must contain POSTGRES_* (or your DB envs)

# --- Optional ---
ENV=development              # if 'production', the agent refuses to run
ENSURE_DOCKER_AUTO=1         # auto-start Colima when Docker isn’t up (0 to disable)
ANTHROPIC_MODEL=claude-sonnet-4-20250514
```

The agent resolves the actual DB volume name as ``<PROJECT>``_``<DB_VOLUME>`` (e.g., myproj_db_data).

---

## Run

Natural language

```bash
go run . "Ramp up the DB and wait for healthy."
go run . "Status"
go run . "Show DB logs (tail: 300)"
go run . "Ramp down the DB"
go run . "Ramp down the DB and delete volume"
go run . "Reset the DB (confirm: RESET myproj)"
```

Build once:

(go run . "build the agent" won't work here. The model would treat that as a prompt and try to call the DB tools already. Go run already builds as part of running; but it will not leave a persistent binary.)

```bash
go build -o compose-db-agent
./compose-db-agent "ramp up db and wait for healthy"
```

---

## What the agent actually does (DB-scoped)

- Up: ``docker compose -p $PROJECT -f $COMPOSE_FILE up -d $DB_SERVICE`` → wait until health is healthy.

- Down: … stop $DB_SERVICE → … rm -f $DB_SERVICE (leaves other services alone).
- Down + delete volume: same as above, then docker volume rm ``<PROJECT>``_``<DB_VOLUME>``.
- Reset: down + delete volume → up DB again → wait healthy → optional seed command.
- Logs: docker logs --tail N <container id of $DB_SERVICE>.

The agent injects env from APP_ENV_FILE and runs with --project-directory $APP_DIR, so Compose variable substitution behaves as if you ran from the app repo.

---

## Troubleshooting

- Go tool mismatch (version "go1.24.5" does not match "go1.24.2"): use one toolchain (prefer devenv/Nix), set go 1.24 in go.mod, run go clean -cache -modcache, ensure which -a go shows a single install.
- Docker not running: agent will try colima start if ENSURE_DOCKER_AUTO != 0. Check docker info, colima status.
- DB never healthy: verify the healthcheck in compose and that POSTGRES_* in APP_ENV_FILE are non-empty.
- “disallowed path” error: ensure COMPOSE_FILE in env matches the path you’re passing (especially with ..).

- Can’t delete volume: confirm the calculated name: ```docker volume ls | grep "<PROJECT>_<DB_VOLUME>"```.

---

## Why not just shell scripts?

- Safer (confirmation for destructive ops, project/path validation, prod guard)
- Smarter UX (natural language → curated tools only)
- Self-healing (auto-starts Colima)
- Deterministic (consistent flags, env injection, health wait)
- Auditable & Extensible (easy to add more tools later; DRY_RUN supported)

---

> **Note:** The canonical repository is [**on GitHub**](https://github.com/vr33ni-dev/db-compose-agent) · [Mirror on GitLab →](https://gitlab.com/vr33ni-personal/db-compose-agent.git) [![Mirror Status](https://github.com/vr33ni-dev/db-compose-agent/actions/workflows/mirror.yml/badge.svg)](https://github.com/vr33ni-dev/db-compose-agent/actions/workflows/mirror.yml)
