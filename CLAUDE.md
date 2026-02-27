# Whitenoise Caster

## Project Overview
Go service that casts looped white noise to Chromecast/Google Home speakers via a mobile-first web UI. Runs on a Hetzner VPS, reaches Chromecasts through a WireGuard tunnel. Audio served publicly via HTTPS.

## Tech Stack
- **Language**: Go 1.23+
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
web/                 — embedded static files (index.html, manifest.json, icon.png)
config.yaml          — example configuration
Dockerfile           — multi-stage build
docker-compose.yml   — app + caddy services
Caddyfile            — reverse proxy config
Makefile             — build targets
```

## Key Patterns
- **Concurrency**: All Controller state guarded by sync.RWMutex. One cast session at a time.
- **Looping**: monitorLoop goroutine polls every 3s, re-issues Load() on IDLE/FINISHED.
- **Reconnect**: 3 consecutive errors trigger full disconnect/reconnect.
- **Auth**: Basic auth wraps API endpoints but NOT the audio endpoint (Chromecast needs unauthenticated access).
- **Audio serving**: http.ServeContent for Range/byte-range support.

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
- `go vet ./...` — lint
- `make run` — build and run with config.yaml
- `make docker-up` — docker compose up

## Style
- Use log/slog for structured logging
- Keep the single-binary, single-file-UI approach
- No frameworks — stdlib HTTP + embedded FS
