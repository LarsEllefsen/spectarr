# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o spectarr ./cmd/spectarr

# Production build (matches Dockerfile)
CGO_ENABLED=0 go build -ldflags="-s -w" -o spectarr ./cmd/spectarr

# Run locally (creates ./config/ for SQLite DB and encryption key)
go run ./cmd/spectarr

# Run Radarr locally for development
docker-compose -f docker-compose.dev.yml up

# Run full stack locally
docker-compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

No test suite exists yet.

## Architecture

Spectarr polls friends' ratings on Specto (a movie/show ratings service) and adds highly-rated movies to Radarr. It runs as a single Go binary serving a web UI on port 6969 with a background scheduler.

**Data flow:**
1. On first boot, `/setup` collects Specto credentials, Radarr URL/key, rating threshold, download mode, and sync source — all other routes redirect there until configured
2. Scheduler runs on a configurable interval (default 60 min) or manual trigger from the dashboard
3. Per run: authenticate to Specto → fetch current user's own rated movies (to exclude) → fetch friends' ratings (all or selected) → deduplicate by TMDB ID keeping highest rating per movie → skip movies already in Radarr, already rejected, or already rated by the current user → in automatic mode add to Radarr immediately; in manual mode queue for review on the dashboard
4. Run results are logged to SQLite and shown on the dashboard

**Package responsibilities:**

| Package | Purpose |
|---|---|
| `cmd/spectarr` | Entry point — wires up store, scheduler, web handler |
| `internal/config` | SQLite store + AES-256-GCM encryption for credentials at rest; pending/rejected movie queues |
| `internal/scheduler` | Background goroutine with ticker; friend rating aggregation with attribution |
| `internal/specto` | Specto API client (login, token refresh, friends list, paginated ratings by user ID) |
| `internal/radarr` | Radarr API client (lookup, add movies, quality profiles, root folders) |
| `internal/web` | chi router, per-page template rendering, HTMX partial responses, setup wizard |

**Storage:** Single SQLite DB (`./config/spectarr.db`). Tables: `config` (key/value, sensitive fields AES-encrypted), `run_log`, `pending_movies` (manual review queue with poster/IMDB/attribution), `rejected_movies` (permanent skip list). Encryption key at `./config/secret.key`.

**SQLite migrations:** New columns are added via `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` in `config.Open()` after schema creation — errors are intentionally ignored (column already exists).

**Frontend:** PicoCSS v2 (dark theme) + HTMX. Templates embedded via `go:embed` and parsed per-request as `base.html + page.html` to avoid named block collisions. The pending movies section renders as a standalone partial (`pending-section` define block in `index.html`) for HTMX swaps on accept/reject.

**Sync modes:** `all_friends` (default) fetches all accepted Specto friends; `selected_friends` filters to a stored comma-separated list of user IDs. Both paths go through `fetchAndMerge` which deduplicates across friends and records which friend had the highest rating (`SuggestedBy`).

**Download modes:** `automatic` adds to Radarr immediately; `manual` queues to `pending_movies` with title, year, poster URL, IMDB ID, and the suggesting friend's name.

**Distribution:** Docker image built and pushed to `ghcr.io/larsellefsen/spectarr` via GitHub Actions (`.github/workflows/docker.yml`) on every push to `master` (`latest`) and on version tags (`v1.2.3` → `1.2.3` and `1.2`).

