# Skillswap DB Agent

A tiny LLM-powered CLI that operates **- currently - only the database** of the *skillswap* project via Docker Compose.  
It wraps all actions in safe, auditable **tools** (no raw shell), with guardrails for destructive resets, and brings services up detached (up -d). (Optional: It can auto-start Colima if Docker isn’t running.)

---

## What it does

- **Ramp up** the DB (`compose up -d`) and **waits for health**
- **Ramp down** the DB (`compose down`) — optionally **wipe volumes**
- **Reset** the DB (down `-v` → up `-d` → wait healthy) with a confirmation phrase
- **Status** (container health) and **logs** (tail)
- **Auto-starts Colima** if Docker isn’t running

---

## Prerequisites

- Go 1.21+  
- Docker (Colima) => `colima` in PATH  
- A Postgres/PostGIS service called `db` in your app’s `docker-compose.yml` with a **healthcheck**

---

## Configuration

Create a `.env` in this repo:

```env
# Required
ANTHROPIC_API_KEY=sk-ant-...        # your Anthropic key
PROJECT=skillswap                   # compose project name (-p)
COMPOSE_FILE=../skillswap/docker-compose.yml
DB_SERVICE=db

# Recommended (since we run compose from this repo)
APP_DIR=../skillswap               # folder with docker-compose.yml
APP_ENV_FILE=../skillswap/.env     # app's .env with POSTGRES_*

# Optional
ANTHROPIC_MODEL=claude-sonnet-4-20250514
ENSURE_DOCKER_AUTO=1               # 1/unset=auto-start Colima; 0=don't
ENV=development                    # if 'production', agent refuses to run
