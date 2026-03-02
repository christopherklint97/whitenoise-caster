# Whitenoise Caster

## Project Overview
Go service that casts looped white noise to Chromecast/Google Home speakers via a mobile-first web UI. Runs on a Hetzner VPS, reaches Chromecasts through an OpenVPN tunnel to an Archer AX1800 router (v1.2). Audio served publicly via HTTPS.

## Tech Stack
- **Language**: Go 1.25+
- **Cast library**: github.com/vishen/go-chromecast (application.Application high-level API)
- **Config**: gopkg.in/yaml.v3
- **Web UI**: Vanilla HTML/CSS/JS, embedded via `//go:embed`
- **Deployment**: Docker + Caddy (auto HTTPS)

## Project Structure
```
main.go              — entrypoint, embeds web/, wires everything, graceful shutdown
config/config.go     — YAML config loading + validation
cast/controller.go   — thread-safe Chromecast controller with monitor loop
handlers/api.go      — HTTP routes (Go 1.22+ ServeMux pattern routing)
handlers/api_test.go — unit tests (function-level mocks, httptest.NewRecorder)
handlers/e2e_test.go — E2E tests (stateful mock, httptest.NewServer, real web files)
web/                 — embedded static files (index.html, manifest.json, icon.png)
config.example.yaml       — example configuration (committed)
config.prod.yaml          — production config with credentials (gitignored)
Dockerfile                — multi-stage build
docker-compose.yml        — dev: app + caddy with Docker networks
docker-compose.prod.yml   — prod: host networking for OpenVPN tunnel access
Caddyfile                 — dev reverse proxy config (example domain)
Caddyfile.prod            — prod reverse proxy with real domain (gitignored)
Makefile                  — build targets (deploy-prod for production)
docs/deployment.md        — full deployment guide
.github/workflows/        — CI (vet + test on push/PR to main)
```

## Key Patterns
- **Concurrency**: All Controller state guarded by sync.RWMutex. One cast session at a time.
- **Looping**: monitorLoop goroutine polls every 3s, re-issues Load() on IDLE/FINISHED.
- **Reconnect**: 3 consecutive errors trigger full disconnect/reconnect.
- **Auth**: Basic auth wraps API endpoints but NOT the audio endpoint (Chromecast needs unauthenticated access).
- **Audio serving**: http.ServeContent for Range/byte-range support.
- **Sleep Timer**: Server-side timer goroutine (timerLoop) fires after a user-set duration to stop playback or reduce volume. Timer state rides along in Status struct via 3s polls. Auto-cancelled on Stop/Play/Close via cancelTimerLocked() in stopLocked().

## go-chromecast API (important signatures)
```go
app.Start(addr string, port int) error
app.Load(filenameOrUrl string, startTime int, contentType string, transcode, detach, forceDetach bool) error
app.Pause() error
app.Unpause() error
app.StopMedia() error
app.Close(stopMedia bool) error
app.Status() (*cast.Application, *cast.Media, *cast.Volume)
```

## Commands
- `go build .` — build binary
- `make test` — run all tests (unit + E2E)
- `make vet` — lint
- `make run` — build and run with config.yaml
- `make dev` — hot-reload with air
- `make docker-up` — docker compose up (dev)
- `make deploy-prod` — docker compose up with production config

## Style
- Use log/slog for structured logging
- Keep the single-binary, single-file-UI approach
- No frameworks — stdlib HTTP + embedded FS
