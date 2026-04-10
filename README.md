# Collection Sync

`collection-sync` keeps Plex collections aligned with Sonarr and Radarr. It can list the contents of a Plex collection, check whether those items already exist in Sonarr or Radarr, and sync missing or unmonitored items.

It works with any Plex collection, including collections maintained by [Maintainerr](https://maintainerr.info).

## Quick Start

1. Copy `.env.example` to `.env`.
2. Set your Plex connection details and the Plex collection names you want to use.
3. Add Sonarr settings for TV workflows and Radarr settings for movie workflows.
4. Start with a dry run before making changes.

```bash
cp .env.example .env
go run ./cmd/collection-sync tv sync --dry-run
go run ./cmd/collection-sync movies sync --dry-run
```

When running locally, the app loads `.env` automatically if the file exists. Environment variables already set in the shell take precedence.

## Run

Run from source:

```bash
go run ./cmd/collection-sync tv list
go run ./cmd/collection-sync tv check
go run ./cmd/collection-sync tv sync --dry-run

go run ./cmd/collection-sync movies list
go run ./cmd/collection-sync movies check
go run ./cmd/collection-sync movies sync --dry-run
```

Build a binary:

```bash
go build -o collection-sync ./cmd/collection-sync
./collection-sync movies sync --dry-run
```

Use the Makefile for common local workflows:

```bash
make build
make test
make test-cover
make test-cover-html
make test-cover-check
make lint
make run ARGS='tv list'
make docker-build
make docker-run ARGS='movies list'
```

If `golangci-lint` is not installed locally, run:

```bash
make lint-install
```

Run with Docker:

```bash
docker build -t collection-sync .
docker run --rm --env-file .env collection-sync
```

The Docker image defaults to the `run` command. Pass arguments to override:

```bash
docker run --rm --env-file .env collection-sync movies list
```

Pull the published container image from GHCR:

```bash
docker pull ghcr.io/dadarrs/collection-sync:latest
docker run --rm --env-file .env ghcr.io/dadarrs/collection-sync:latest
docker run --rm --env-file .env ghcr.io/dadarrs/collection-sync:latest movies list
```

Use Docker Compose:

```yaml
services:
  collection-sync:
    image: ghcr.io/dadarrs/collection-sync:latest
    env_file:
      - .env
    restart: unless-stopped
```

The container exits after a single sync unless `INTERVAL` is set in `.env`. To run a one-off command with Compose, override the default command:

```bash
docker compose run --rm collection-sync movies list
docker compose run --rm collection-sync tv sync --dry-run
```

## Commands

| Command | Purpose |
| --- | --- |
| `run [--dry-run]` | Sync both TV and movie collections in one command. Skips whichever lacks API config. Repeats on `INTERVAL` if set. |
| `tv list` | List shows and seasons in `PLEX_TV_COLLECTION`. |
| `tv check` | Compare the TV collection against Sonarr and report status per item. |
| `tv sync [number] [--dry-run]` | Add missing TV items to Sonarr or update monitoring for existing matches. Pass `number` to sync a single row from `tv list`. |
| `movies list` | List movies in `PLEX_MOVIE_COLLECTION`. |
| `movies check` | Compare the movie collection against Radarr and report status per item. |
| `movies sync [number] [--dry-run]` | Add missing movies to Radarr or update monitoring for existing matches. Pass `number` to sync a single row from `movies list`. |

`--dry-run` previews changes without writing to Sonarr or Radarr.

## Configuration

Copy `.env.example` to `.env` and set the variables below.

| Variable | Required For | Notes |
| --- | --- | --- |
| `PLEX_URL` | All commands | Base URL for Plex, for example `http://localhost:32400`. |
| `PLEX_TOKEN` | All commands | Plex token with access to the target libraries. |
| `PLEX_TV_COLLECTION` | `tv` commands | Name of the Plex TV collection to inspect or sync. |
| `PLEX_MOVIE_COLLECTION` | `movies` commands | Name of the Plex movie collection to inspect or sync. |
| `SONARR_URL` | `tv check`, `tv sync` | Base URL for Sonarr. |
| `SONARR_API_KEY` | `tv check`, `tv sync` | Sonarr API key. |
| `SONARR_ROOT_FOLDER` | `tv sync` | Optional. If unset, the app uses the only available Sonarr root folder. |
| `SONARR_QUALITY_PROFILE` | `tv sync` | Optional. If unset, the app uses the only available Sonarr quality profile. |
| `RADARR_URL` | `movies check`, `movies sync` | Base URL for Radarr. |
| `RADARR_API_KEY` | `movies check`, `movies sync` | Radarr API key. |
| `RADARR_ROOT_FOLDER` | `movies sync` | Optional. If unset, the app uses the only available Radarr root folder. |
| `RADARR_QUALITY_PROFILE` | `movies sync` | Optional. If unset, the app uses the only available Radarr quality profile. |
| `SEARCH_ADDED` | `sync` commands | Optional. When `true`, queue a Sonarr or Radarr search after content is added or newly enabled for monitoring. |
| `SEARCH_EXISTING` | `sync` commands | Optional. When `true`, queue a Sonarr or Radarr search even when the requested item already exists. |
| `INTERVAL` | `run` | Optional. Repeat the sync on this interval, for example `10m`, `1h`, `6h`, `3d`. If unset, `run` syncs once and exits. |

Reliable matching depends on Plex metadata exposing TVDB IDs for TV items and TMDB IDs for movies.
