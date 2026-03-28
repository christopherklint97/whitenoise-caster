# Deployment Guide

Whitenoise Caster runs on a Raspberry Pi on the home LAN. A Cloudflare Tunnel provides public HTTPS access for the web UI. Chromecasts fetch audio directly from the Pi over LAN.

## Architecture

```
                    Cloudflare Tunnel
Internet ◄──────────────────────────────► Raspberry Pi (home LAN)
                                          192.168.1.x
  Your phone                              Docker:
  noise.example.com ──► Cloudflare ──►    - app (:8080)
                        (HTTPS)

                                          Also on LAN:
                                          - Speaker A (192.168.1.100)
                                          - Speaker B (192.168.1.101)
```

**Key insight:** The Pi is on the same LAN as the Chromecasts, so cast control (TCP 8009) and audio fetching happen directly over the local network — no VPN or tunneling needed for that path. Cloudflare Tunnel only handles external web UI access.

## Prerequisites

- Raspberry Pi (3B+ or newer) with Raspberry Pi OS (64-bit recommended)
- Docker and Docker Compose installed on the Pi
- A `whitenoise.mp3` audio file
- A domain with DNS managed by Cloudflare
- A Cloudflare account (free tier works)

## Step 1: Set Up the Raspberry Pi

### Install Raspberry Pi OS

Use the [Raspberry Pi Imager](https://www.raspberrypi.com/software/) to flash Raspberry Pi OS Lite (64-bit) to an SD card. Enable SSH during setup.

### Install Docker

```bash
ssh pi@<PI_IP>

curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
# Log out and back in for group change to take effect
```

### Assign a static IP

Give the Pi a static LAN IP so the Chromecast audio URL doesn't change. Configure this in your router's DHCP reservation settings (preferred) or in the Pi's network config.

## Step 2: Set Up Cloudflare Tunnel

Cloudflare Tunnel (`cloudflared`) creates an outbound connection from the Pi to Cloudflare's edge, so no inbound ports need to be opened on your router.

### Install cloudflared

```bash
# On the Pi
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64 -o /usr/local/bin/cloudflared
chmod +x /usr/local/bin/cloudflared
```

### Authenticate and create a tunnel

```bash
cloudflared tunnel login
# This opens a browser to authorize with your Cloudflare account

cloudflared tunnel create whitenoise
# Note the tunnel ID (e.g., abc123-def456-...)
```

### Configure the tunnel

Create `/etc/cloudflared/config.yml`:

```yaml
tunnel: <TUNNEL_ID>
credentials-file: /root/.cloudflared/<TUNNEL_ID>.json

ingress:
  - hostname: noise.example.com
    service: http://localhost:8080
  - service: http_status:404
```

Replace `noise.example.com` with your actual domain and `<TUNNEL_ID>` with the ID from the create step.

### Add a DNS route

```bash
cloudflared tunnel route dns whitenoise noise.example.com
```

This creates a CNAME record in Cloudflare DNS pointing your domain to the tunnel.

### Run as a systemd service

```bash
cloudflared service install
systemctl enable --now cloudflared
```

Verify:

```bash
systemctl status cloudflared
curl https://noise.example.com  # should reach the app once deployed
```

## Step 3: Deploy the Application

### Clone the repo

```bash
git clone <your-repo-url> /opt/whitenoise-caster
cd /opt/whitenoise-caster
```

### Add the audio file

```bash
# From your local machine
scp whitenoise.mp3 pi@<PI_IP>:/opt/whitenoise-caster/
```

### Create the production config

```bash
cp config.example.yaml config.prod.yaml
nano config.prod.yaml
```

Set your speaker IPs, set `audio_url` to `http://<PI_LAN_IP>:8080` (Chromecasts fetch audio over LAN, not through the tunnel), and set `auth.username` / `auth.password`.

`config.prod.yaml` is gitignored — do not commit it.

### Start everything

```bash
cd /opt/whitenoise-caster
make deploy-prod
# or: docker compose -f docker-compose.prod.yml up -d --build
```

### Verify

```bash
# Check containers are running
docker compose -f docker-compose.prod.yml ps

# Watch logs
docker compose -f docker-compose.prod.yml logs -f

# Test locally on the Pi
curl http://localhost:8080/api/status

# Test through the tunnel
curl https://noise.example.com/api/status
```

## Network Flow

When you hit "Play" in the web UI:

1. Your phone sends `POST /api/play` to `noise.example.com`
2. Cloudflare Tunnel forwards the request to the Pi's app on `localhost:8080`
3. The app connects to the Chromecast at `192.168.1.x:8009` directly over LAN
4. The app tells the Chromecast to load audio from `http://<PI_IP>:8080/audio/<secret>/whitenoise.mp3`
5. The Chromecast fetches the audio directly from the Pi over LAN (no internet round-trip)
6. The app's monitor loop polls the Chromecast every 3s and re-loads the track when it finishes (looping)

## Updating

Pull the latest code and rebuild locally:

```bash
cd /opt/whitenoise-caster
git pull
make deploy-prod
# or: docker compose -f docker-compose.prod.yml up -d --build
```

## Maintenance

### Restarting

```bash
cd /opt/whitenoise-caster
docker compose -f docker-compose.prod.yml restart
```

### Viewing logs

```bash
# App logs
docker compose -f docker-compose.prod.yml logs -f

# Cloudflare Tunnel logs
journalctl -u cloudflared -f
```

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Tunnel not connecting | `systemctl status cloudflared` and check `/etc/cloudflared/config.yml` |
| Domain not resolving | Verify CNAME exists: `dig +short noise.example.com` should return a `cfargotunnel.com` address |
| App unreachable through tunnel | Ensure app is running on port 8080: `curl http://localhost:8080/api/status` |
| Chromecast can't fetch audio | `audio_url` must use the Pi's LAN IP, not the public domain. Test: `curl http://<PI_IP>:8080/audio/<secret>/whitenoise.mp3 -I` from a LAN device |
| Cast control fails | Chromecast may be off or on a different IP. Verify IPs in `config.prod.yaml` match actual devices. Try `ping 192.168.1.x` from the Pi |
| Build fails on Pi | Check Docker and disk space: `docker system df`, `df -h` |

## Security Notes

- The audio endpoint is **intentionally unauthenticated** — the Chromecast needs to fetch it without credentials. The `secret_path` in the URL provides obscurity.
- All other API endpoints are behind basic auth.
- Cloudflare Tunnel provides HTTPS termination and DDoS protection.
- No inbound ports need to be opened on your router — the tunnel connects outbound.
- `config.prod.yaml` is gitignored. Never commit files containing credentials.
