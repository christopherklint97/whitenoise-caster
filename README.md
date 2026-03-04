# Whitenoise Caster

A Go service that plays looped white noise on Chromecast/Google Home speakers, controlled via a mobile-first web UI.

## Architecture

- **Go backend** wrapping [go-chromecast](https://github.com/vishen/go-chromecast) for cast control
- **Embedded web UI** — dark-theme PWA with play/pause, volume, and sleep timer
- **Runs on a Raspberry Pi** on the same LAN as the Chromecasts
- **Cloudflare Tunnel** provides public HTTPS access to the web UI

## Quick Start

```bash
# Place your white noise MP3 in the project root
cp /path/to/whitenoise.mp3 .

# Edit config.yaml with your speaker IPs and audio URL
vim config.yaml

# Run
go run . -config config.yaml
```

Open `http://localhost:8080`, select a speaker, and press play.

## Development

```bash
# Install frontend dependencies
npm install

# Run all tests (Go + frontend)
make test
make web-test

# Lint
make vet

# Hot-reload (requires air) — watches Go, TS, and HTML files
make dev
```

The frontend is written in TypeScript under `web/src/` and bundled with esbuild into `web/app.js` (gitignored). The `make build` and `make test` targets handle this automatically.

CI runs `npm test`, `npm run build`, `go vet`, and `make test` on every push/PR to `main` via GitHub Actions.

## Configuration

See `config.example.yaml` for all options:

- **speakers** — list of Chromecast devices (name + IP)
- **audio_file** — path to the MP3 file
- **audio_url** — base URL the Chromecast will use to fetch audio (Pi's LAN IP)
- **listen_addr** — HTTP listen address (default `:8080`)
- **auth** — optional basic auth for the web UI
- **secret_path** — secret URL segment for the audio endpoint (auto-generated if empty)

## Deployment

Runs on a Raspberry Pi with Cloudflare Tunnel for public HTTPS access. See [docs/deployment.md](docs/deployment.md) for the full guide.

```bash
make deploy-prod
```

## How It Works

1. User selects a speaker and presses play in the web UI
2. Server connects to the Chromecast directly over the home LAN
3. Server tells the Chromecast to load the audio URL (Pi's LAN IP)
4. The Chromecast fetches audio directly from the Pi over LAN
5. A monitor goroutine polls every 3s and re-loads the audio when it finishes (looping)
6. On persistent errors, it performs a full reconnect

## Sleep Timer

Set a sleep timer to automatically stop playback or reduce volume after a set duration (up to 12 hours). The timer runs server-side so it works even after closing your phone/browser. Set via iOS-style scroll wheels in the web UI. Auto-cancels when playback is stopped.
