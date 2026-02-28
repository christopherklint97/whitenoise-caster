# Deployment Guide

Whitenoise Caster runs on a Hetzner VPS and reaches Chromecasts on a home LAN through an OpenVPN tunnel provided by a TP-Link Archer AX1800 (hardware v1.2).

## Architecture

```
┌──────────────────────┐       OpenVPN tunnel       ┌───────────────────────┐
│  Hetzner VPS         │◄──────────────────────────►│  Archer AX1800 (v1.2) │
│  (OpenVPN client)    │       10.8.0.x             │  (OpenVPN server)     │
│                      │                             │                       │
│  Docker (host net):  │                             │  Home LAN:            │
│  - app    (:8080)    │─── TCP 8009 ──────────────►│  - Living Room        │
│  - caddy  (:80/443)  │    via 192.168.1.x         │    192.168.1.100       │
│                      │                             │  - Bedroom            │
│                      │◄── HTTPS audio fetch ──────│    192.168.1.101      │
└──────────────────────┘                             └───────────────────────┘
         ▲
         │ HTTPS
         │ noise.example.com
    Your phone
```

**Why OpenVPN instead of WireGuard?** The Archer AX1800 hardware v1.2 does not support WireGuard. V1 firmware only provides OpenVPN and PPTP VPN servers. OpenVPN is the secure choice of the two.

## Prerequisites

- Hetzner VPS (any plan with a public IPv4)
- Domain: `noise.example.com` with DNS managed somewhere you can add an A record
- TP-Link Archer AX1800 (v1.2) with latest firmware
- A `whitenoise.mp3` audio file
- SSH access to the VPS

## Step 1: Configure OpenVPN Server on the Router

1. Open the router admin panel at `http://tplinkwifi.net` or `http://192.168.1.1`
2. Log in with your TP-Link ID or router password
3. Navigate to **Advanced > VPN Server > OpenVPN**
4. Check **Enable VPN Server**
5. Set **Service Type** to **UDP** and **Service Port** to **1194** (default)
6. Set **VPN Subnet/Netmask** to `10.8.0.0 / 255.255.255.0`
7. Set **Client Access** to **Home Network Only** (the VPS only needs to reach LAN devices)
8. Click **Generate** to create a certificate (if not already done)
9. Click **Save**
10. Click **Export** to download the `.ovpn` client configuration file

The exported `.ovpn` file contains the server address, certificates, and keys needed for the VPS to connect.

### Dynamic DNS (if your home IP changes)

If your ISP assigns a dynamic public IP:

1. On the router, go to **Advanced > Network > Dynamic DNS**
2. Register with a supported DDNS provider (No-IP, DynDNS, etc.)
3. Configure it on the router so the hostname always resolves to your current home IP
4. Edit the exported `.ovpn` file and replace the IP in the `remote` line with your DDNS hostname

## Step 2: Provision the Hetzner VPS

### System setup

```bash
ssh root@<VPS_IP>

# Update system
apt update && apt upgrade -y

# Install OpenVPN
apt install -y openvpn

# Install Docker
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker

# Install Docker Compose plugin
apt install -y docker-compose-plugin
```

### Firewall (if ufw is active)

```bash
ufw allow 22/tcp    # SSH
ufw allow 80/tcp    # HTTP (Caddy redirect to HTTPS)
ufw allow 443/tcp   # HTTPS
ufw enable
```

No inbound VPN port is needed on the VPS since it acts as the OpenVPN **client** connecting outbound to the router.

## Step 3: Install OpenVPN Client on the VPS

1. Copy the `.ovpn` file exported from the router to the VPS:

```bash
scp ~/Downloads/client.ovpn root@<VPS_IP>:/etc/openvpn/client.conf
```

> The file must be named `client.conf` (not `.ovpn`) for systemd to manage it.

2. Enable and start the OpenVPN client service:

```bash
systemctl enable --now openvpn@client
```

3. Verify the tunnel is up:

```bash
# Check the tunnel interface exists
ip addr show tun0

# Ping a Chromecast through the tunnel
ping -c 3 192.168.1.100
```

If the ping succeeds, your VPS can reach your home LAN. If it fails, check:
- Router firewall / VPN server status
- Whether your home router's public IP or DDNS hostname is correct in the `.ovpn` file
- `journalctl -u openvpn@client` for connection errors

## Step 4: DNS Record

Add an A record for your domain pointing to the Hetzner VPS public IP:

```
noise.example.com  →  A  →  <VPS_PUBLIC_IP>
```

Set this up wherever you manage DNS for `example.com`. TTL of 300 (5 min) is fine.

Verify:

```bash
dig +short noise.example.com
# Should return your VPS IP
```

## Step 5: Deploy the Application

### Clone the repo

```bash
git clone <your-repo-url> /opt/whitenoise-caster
cd /opt/whitenoise-caster
```

### Add the audio file

```bash
# From your local machine
scp whitenoise.mp3 root@<VPS_IP>:/opt/whitenoise-caster/
```

### Create the production config

Create the production config and Caddyfile from the examples:

```bash
cp config.example.yaml config.prod.yaml
nano config.prod.yaml
```

Fill in your real speaker IPs, set `audio_url` to your real domain (`https://your-domain.com`), and set `auth.username` / `auth.password`.

```bash
cp Caddyfile Caddyfile.prod
nano Caddyfile.prod
```

Replace `noise.example.com` with your real domain and change `app:8080` to `localhost:8080` (since production uses host networking).

Both files are gitignored — do not commit them.

### Production files

The production compose file uses `network_mode: host` so both containers share the VPS network stack. This gives the app direct access to the `tun0` interface (OpenVPN tunnel) to reach Chromecasts, and lets Caddy bind to ports 80/443 directly.

| File | Purpose | Committed? |
|------|---------|------------|
| `docker-compose.prod.yml` | Production compose (host networking) | Yes |
| `config.example.yaml` | Template for creating config files | Yes |
| `Caddyfile` | Template Caddyfile (example domain) | Yes |
| `config.prod.yaml` | Production config with credentials | **No** (gitignored) |
| `Caddyfile.prod` | Production Caddyfile with real domain | **No** (gitignored) |

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

# Test the API
curl https://noise.example.com/api/status
```

## Network Flow

When you hit "Play" in the web UI:

1. Your phone sends `POST /api/play` to `noise.example.com` (Hetzner VPS)
2. Caddy terminates TLS and proxies to the app on `localhost:8080`
3. The app connects to the Chromecast at `192.168.1.100:8009` through the OpenVPN tunnel (`tun0`)
4. The app tells the Chromecast to load audio from `https://noise.example.com/audio/<secret>/whitenoise.mp3`
5. The Chromecast fetches the audio over the public internet (Chromecast → Google DNS → your VPS)
6. The app's monitor loop polls the Chromecast every 3s and re-loads the track when it finishes (looping)

## Maintenance

### Restarting

```bash
cd /opt/whitenoise-caster
docker compose -f docker-compose.prod.yml restart        # restart containers
docker compose -f docker-compose.prod.yml up -d --build  # rebuild and restart
```

### Updating

```bash
cd /opt/whitenoise-caster
git pull
docker compose -f docker-compose.prod.yml up -d --build
```

### Checking the VPN tunnel

```bash
# OpenVPN status
systemctl status openvpn@client

# Tunnel interface
ip addr show tun0

# Test connectivity to home LAN
ping 192.168.1.100
```

### Viewing logs

```bash
# App + Caddy logs
docker compose -f docker-compose.prod.yml logs -f

# OpenVPN logs
journalctl -u openvpn@client -f
```

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Can't reach Chromecasts | `ping 192.168.1.100` — is the VPN tunnel up? Check `systemctl status openvpn@client` |
| VPN connects but no LAN access | Router VPN setting must be "Home Network Only" or "Internet and Home Network" — verify client access mode |
| Chromecast can't fetch audio | The audio URL must be publicly reachable. Test: `curl https://noise.example.com/audio/<secret>/whitenoise.mp3 -I` from any network |
| Caddy won't start | Port 80 or 443 already in use? Check with `ss -tlnp | grep -E ':80|:443'` |
| TLS cert fails | DNS must be propagated. Check: `dig +short noise.example.com`. Caddy needs port 80 open for the ACME HTTP challenge |
| Connection timeouts after home IP change | Set up DDNS on the router and use the DDNS hostname in the `.ovpn` config |
| App starts but "connect: connection refused" | Chromecast may be off or on a different IP. Verify IPs in `config.yaml` match actual devices |

## Security Notes

- The audio endpoint is **intentionally unauthenticated** — the Chromecast needs to fetch it without credentials. The `secret_path` in the URL provides obscurity.
- All other API endpoints are behind basic auth.
- Caddy enforces HTTPS with automatic certificate management.
- The OpenVPN tunnel encrypts all traffic between the VPS and your home network.
- `config.prod.yaml` is gitignored. Never commit files containing credentials. Use `config.example.yaml` as a template.
