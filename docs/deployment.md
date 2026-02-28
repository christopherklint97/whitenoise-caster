# Deployment Guide

Whitenoise Caster runs on a Hetzner VPS and reaches Chromecasts on a home LAN through an OpenVPN tunnel provided by a TP-Link Archer AX1800 (hardware v1.2).

## Architecture

```
┌──────────────────────┐       OpenVPN tunnel       ┌───────────────────────┐
│  Hetzner VPS         │◄──────────────────────────►│  Archer AX1800 (v1.2) │
│  (OpenVPN client)    │       10.8.0.x             │  (OpenVPN server)     │
│                      │                             │                       │
│  k3s (hostNetwork):  │                             │  Home LAN:            │
│  - app pod (:8080)   │─── TCP 8009 ──────────────►│  - Speaker A          │
│  - traefik (:80/443) │    via 192.168.0.x         │    192.168.0.x        │
│                      │                             │  - Speaker B          │
│                      │◄── HTTPS audio fetch ──────│    192.168.0.x        │
└──────────────────────┘                             └───────────────────────┘
         ▲
         │ HTTPS
         │ noise.example.com
    Your phone
```

**Why OpenVPN instead of WireGuard?** The Archer AX1800 hardware v1.2 does not support WireGuard. V1 firmware only provides OpenVPN and PPTP VPN servers. OpenVPN is the secure choice of the two.

## Prerequisites

- Hetzner VPS (any plan with a public IPv4)
- Domain with DNS managed somewhere you can add an A record
- TP-Link Archer AX1800 (v1.2) with latest firmware
- **A public IP from your ISP** (not behind CGNAT — see below)
- A `whitenoise.mp3` audio file
- SSH access to the VPS

### CGNAT Check (Important)

The VPS connects **inbound** to the router's OpenVPN server. This requires your home router to have a real public IP. Many ISPs use Carrier-Grade NAT (CGNAT), which makes inbound connections impossible.

**To check if you're behind CGNAT:**

1. Log in to the router admin panel at `http://192.168.0.1`
2. Go to **Network > Internet** and note the **IP Address** (WAN IP)
3. From a device on your home WiFi, visit `https://ifconfig.me` and note the public IP

If the WAN IP and public IP **match** — you have a real public IP. You're good.

If the WAN IP is **different** (typically in the `100.64.0.0/10` range, e.g. `100.x.x.x`) — you're behind CGNAT. Contact your ISP and request a public IP address. Some ISPs provide this for free, others charge a small fee. Without a public IP, the VPS cannot reach the router's VPN server.

## Step 1: Configure Dynamic DNS on the Router

If your ISP assigns a dynamic public IP (most do), set up DDNS so the VPS can always find the router:

1. On the router, go to **Advanced > Network > Dynamic DNS**
2. TP-Link provides a free built-in DDNS service — register a hostname (e.g. `myhome.tplinkdns.com`)
3. Enable the DDNS entry and save

Verify from any machine:

```bash
dig +short myhome.tplinkdns.com
# Should return your home public IP
```

## Step 2: Configure OpenVPN Server on the Router

1. Open the router admin panel at `http://192.168.0.1`
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

**Edit the exported `.ovpn` file:** Replace the IP in the `remote` line with your DDNS hostname:

```
remote myhome.tplinkdns.com 1194
```

### If port 1194 is blocked

Some ISPs block well-known VPN ports. If the VPS can't connect on 1194:

1. Try a different port on the router (e.g. `51194`) — the Archer restricts ports to 1024-65535
2. Test reachability from the VPS: `nmap -Pn -sU -p 51194 myhome.tplinkdns.com`
3. Update both the router's Service Port and the `remote` line in the `.ovpn` file

## Step 3: Provision the Hetzner VPS

### System setup

```bash
ssh root@<VPS_IP>

# Update system
apt update && apt upgrade -y

# Install OpenVPN
apt install -y openvpn

# Install k3s
curl -sfL https://get.k3s.io | sh -

# Verify k3s is running
kubectl get nodes

# Install cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml

# Wait for cert-manager to be ready
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=120s
```

### Firewall (if ufw is active)

```bash
ufw allow 22/tcp    # SSH
ufw allow 80/tcp    # HTTP (Traefik ACME + redirect)
ufw allow 443/tcp   # HTTPS
ufw allow 6443/tcp  # k3s API server (optional, for remote kubectl)
ufw enable
```

No inbound VPN port is needed on the VPS since it acts as the OpenVPN **client** connecting outbound to the router.

## Step 4: Install OpenVPN Client on the VPS

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

# Ping the router's LAN IP through the tunnel
ping -c 3 192.168.0.1
```

If the ping succeeds, your VPS can reach your home LAN. If it fails, see the Troubleshooting section below.

## Step 5: DNS Record

Add an A record for your domain pointing to the Hetzner VPS public IP:

```
noise.example.com  →  A  →  <VPS_PUBLIC_IP>
```

Set this up wherever you manage DNS. TTL of 300 (5 min) is fine.

Verify:

```bash
dig +short noise.example.com
# Should return your VPS IP
```

## Step 6: Deploy the Application

### Place the audio file

```bash
mkdir -p /opt/whitenoise-caster
scp whitenoise.mp3 root@<VPS_IP>:/opt/whitenoise-caster/
```

### Create the production config

On your local machine, create `config.prod.yaml` from the example:

```bash
cp config.example.yaml config.prod.yaml
nano config.prod.yaml
```

Fill in your real speaker IPs (on the `192.168.0.x` subnet), set `audio_file` to `/audio/whitenoise.mp3`, set `audio_url` to your real domain (e.g. `https://noise.example.com`), and set `auth.username` / `auth.password`.

### Update k8s manifests

Edit `k8s/ingress.yaml` and replace `noise.example.com` with your real domain (both in `tls.hosts` and `rules.host`).

Edit `k8s/cert-manager/clusterissuer.yaml` and replace `you@example.com` with your real email for Let's Encrypt notifications.

### Create secrets and apply manifests

```bash
# Create the namespace
make k8s-apply

# Create the config secret from config.prod.yaml
make k8s-secret

# Create the GHCR image pull secret
# Create a GitHub PAT with read:packages scope at https://github.com/settings/tokens
make k8s-secret-pull GHCR_USER=<your-github-username> GHCR_TOKEN=<your-pat>
```

### Verify

```bash
# Check pod status
make k8s-status

# Watch logs
make k8s-logs

# Test the API
curl -v https://noise.example.com/api/status -u user:pass

# Test the audio endpoint
curl -I https://noise.example.com/audio/<secret>/whitenoise.mp3
```

### Enable CI deploy

Set the following secrets in your GitHub repository (Settings > Secrets and variables > Actions):

| Secret | Value |
|--------|-------|
| `VPS_HOST` | VPS public IP or hostname |
| `VPS_USER` | SSH user (e.g. `root`) |
| `VPS_SSH_KEY` | Private SSH key for the VPS |

After setting these, every push to `main` will automatically build, push, and deploy.

## Network Flow

When you hit "Play" in the web UI:

1. Your phone sends `POST /api/play` to `noise.example.com` (Hetzner VPS)
2. Traefik terminates TLS and routes to the app pod on port 8080
3. The app connects to the Chromecast at `192.168.0.x:8009` through the OpenVPN tunnel (`tun0`)
4. The app tells the Chromecast to load audio from `https://noise.example.com/audio/<secret>/whitenoise.mp3`
5. The Chromecast fetches the audio over the public internet (Chromecast -> Google DNS -> your VPS)
6. The app's monitor loop polls the Chromecast every 3s and re-loads the track when it finishes (looping)

## Maintenance

### Restarting

```bash
make k8s-deploy
# or: kubectl rollout restart deployment/whitenoise-caster -n whitenoise
```

### Updating

Updates happen automatically. When you push to `main`, GitHub Actions builds a new image, pushes it to GHCR, and SSHes into the VPS to run `kubectl rollout restart`. The pod pulls the latest image (via `imagePullPolicy: Always`) and restarts.

To manually trigger an update:

```bash
make k8s-deploy
```

### Checking the VPN tunnel

```bash
# OpenVPN status
systemctl status openvpn@client

# Tunnel interface
ip addr show tun0

# Test connectivity to home LAN
ping 192.168.0.1

# Verify pod can see the tunnel
kubectl exec -n whitenoise deployment/whitenoise-caster -- ip addr show tun0
```

### Viewing logs

```bash
# App logs
make k8s-logs
# or: kubectl logs -f deployment/whitenoise-caster -n whitenoise

# OpenVPN logs
journalctl -u openvpn@client -f

# Traefik logs
kubectl logs -f -n kube-system -l app.kubernetes.io/name=traefik
```

### Checking resources

```bash
make k8s-status
# or: kubectl get pods,svc,ingress -n whitenoise

# Detailed pod info
kubectl describe pod -n whitenoise -l app=whitenoise-caster

# Check TLS certificate
kubectl get certificate -n whitenoise
```

### Rollback

```bash
# Undo last deployment
kubectl rollout undo deployment/whitenoise-caster -n whitenoise

# Or pin a specific image SHA
kubectl set image deployment/whitenoise-caster \
  whitenoise-caster=ghcr.io/christopherklint97/whitenoise-caster:<sha> \
  -n whitenoise
```

## Troubleshooting

### VPN tunnel won't connect

**Check the basics:**

```bash
# On the VPS — check OpenVPN client status
systemctl status openvpn@client
journalctl -u openvpn@client -n 50 --no-pager

# Verify the TUN kernel module is loaded
lsmod | grep tun
# If empty: sudo modprobe tun
```

**TLS handshake timeout (most common issue):**

If you see `TLS Error: TLS key negotiation failed to occur within 60 seconds`, the VPS cannot reach the router's OpenVPN server. Check in this order:

1. **CGNAT** — Verify you have a real public IP (see CGNAT Check above). This is the most common cause. If your router's WAN IP is in `100.64.0.0/10`, no inbound connections will work.

2. **DDNS stale** — Check that your DDNS hostname resolves to your actual public IP:
   ```bash
   dig +short myhome.tplinkdns.com
   ```
   Compare with your actual IP at `https://ifconfig.me` from your home network.

3. **OpenVPN server down on router** — Log in to the router admin panel and check **Advanced > VPN Server > OpenVPN** is enabled. Toggle it off and on if needed. Try rebooting the router.

4. **Port blocked by ISP** — Test reachability from the VPS:
   ```bash
   nmap -Pn -sU -p 1194 myhome.tplinkdns.com
   ```
   If closed, try a different port (see "If port 1194 is blocked" above).

5. **Test from LAN** — Install the OpenVPN Connect app on your phone, import the `.ovpn` config with `remote 192.168.0.1 1194`, and test from your home WiFi. If this works but the VPS can't connect, the issue is between the internet and your router (CGNAT, ISP firewall, or port blocking).

**VPN connected but `tun0` missing:**

```bash
# Check all tunnel interfaces
ip addr | grep -E "tun|tap"

# Ensure TUN module is loaded
sudo modprobe tun
sudo systemctl restart openvpn@client
```

### Pod issues

| Symptom | Check |
|---------|-------|
| Pod stuck in `ImagePullBackOff` | GHCR pull secret missing or expired. Recreate: `make k8s-secret-pull GHCR_USER=... GHCR_TOKEN=...` |
| Pod stuck in `CrashLoopBackOff` | Check logs: `make k8s-logs`. Usually a config error — verify `config.prod.yaml` and re-run `make k8s-secret` |
| Pod running but can't reach Chromecast | Verify `hostNetwork: true` is set and `tun0` is visible inside the pod |
| Port 8080 already in use | Another process (Docker?) is binding 8080. Stop it first |

### Other issues

| Symptom | Check |
|---------|-------|
| VPN connects but no LAN access | Router VPN setting must be "Home Network Only" or "Internet and Home Network" — verify client access mode |
| Chromecast can't fetch audio | The audio URL must be publicly reachable. Test: `curl https://noise.example.com/audio/<secret>/whitenoise.mp3 -I` from any network |
| Traefik won't start | Port 80 or 443 already in use? Check with `ss -tlnp \| grep -E ':80\|:443'` |
| TLS cert not issued | Check cert-manager logs: `kubectl logs -n cert-manager -l app=cert-manager`. DNS must be propagated and port 80 must be open for HTTP-01 challenge |
| App starts but "connect: connection refused" | Chromecast may be off or on a different IP. Verify IPs in `config.yaml` match actual devices |

### VPS firewall reference

The VPS only needs these ports open:

```bash
ufw status
# 22/tcp   — SSH
# 80/tcp   — HTTP (Traefik ACME + redirect)
# 443/tcp  — HTTPS
# 6443/tcp — k3s API (optional)
```

No inbound VPN port is needed. The VPS connects **outbound** to the router as an OpenVPN client. UFW's default rules allow established/related return traffic.

## Security Notes

- The audio endpoint is **intentionally unauthenticated** — the Chromecast needs to fetch it without credentials. The `secret_path` in the URL provides obscurity.
- All other API endpoints are behind basic auth.
- Traefik enforces HTTPS with automatic certificate management via cert-manager.
- The OpenVPN tunnel encrypts all traffic between the VPS and your home network.
- Config secrets are stored as Kubernetes Secrets in-cluster. Never commit `config.prod.yaml`.
- The router's SPI Firewall should remain enabled. "Respond to Pings from WAN" can stay disabled — it's not needed for OpenVPN.

## Migration from Docker Compose

If migrating from the previous Docker Compose setup:

1. Install k3s and cert-manager (see Step 3)
2. Stop Caddy to free ports 80/443: `docker compose -f docker-compose.prod.yml stop caddy`
3. Apply k8s manifests and create secrets (see Step 6)
4. Stop the Docker app to free port 8080: `docker compose -f docker-compose.prod.yml stop app`
5. Verify the k3s pod starts and TLS cert is issued
6. Test the full flow: UI, play/pause/stop, audio endpoint
7. Set GitHub Actions secrets to enable CI deploy
8. Tear down Docker Compose: `docker compose -f docker-compose.prod.yml down`

**Rollback during migration:** Docker Compose is still installed. Scale k3s to 0 replicas and restart Docker Compose with `make deploy-prod`.
