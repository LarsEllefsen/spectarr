# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o spectarr ./cmd/spectarr

# Production build (matches Dockerfile)
CGO_ENABLED=0 go build -ldflags="-s -w" -o spectarr ./cmd/spectarr

# Run locally (creates ./data/ for SQLite DB and encryption key)
DATA_DIR=./data go run ./cmd/spectarr

# Docker
docker-compose up --build
```

No test suite exists yet.

## Architecture

Spectarr polls Specto (a movie/show ratings service) and automatically adds highly-rated movies to Radarr. It runs as a single Go binary serving a web UI on port 8080 with a background scheduler.

**Data flow:**
1. User configures Specto credentials, Radarr URL/key, rating threshold, and poll interval via the settings page
2. Scheduler runs on a configurable interval (default 60 min) or manual trigger from the dashboard
3. Per run: authenticate to Specto → fetch paginated movie ratings → compare against Radarr's monitored movies by TMDB ID → add new ones that meet the threshold
4. Run results (movies added, errors) are logged to SQLite and shown on the dashboard

**Package responsibilities:**

| Package | Purpose |
|---|---|
| `cmd/spectarr` | Entry point — wires up store, scheduler, web handler |
| `internal/config` | SQLite store + AES-256-GCM encryption for credentials at rest |
| `internal/scheduler` | Background goroutine with ticker; supports manual trigger via channel |
| `internal/specto` | Specto API client (login, token refresh, paginated ratings fetch) |
| `internal/radarr` | Radarr API client (lookup, add movies, fetch quality profiles & root folders) |
| `internal/web` | chi router, HTML template rendering (base/index/settings), HTMX form handling |

**Storage:** Single SQLite DB (`$DATA_DIR/spectarr.db`). Two tables: `config` (one row, encrypted sensitive fields) and `run_log`. Encryption key is stored in `$DATA_DIR/secret.key` (AES-256-GCM, 32 bytes, 0600 permissions).

**Frontend:** PicoCSS + HTMX. Templates are embedded via `go:embed`. HTMX forms POST to the same routes; handlers detect `HX-Request` header to return partial vs full-page responses.

**Environment variables:**
- `DATA_DIR` (default `./data`) — path for the SQLite DB and secret key
