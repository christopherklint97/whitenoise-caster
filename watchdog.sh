#!/bin/sh
# External recovery watchdog for whitenoise-caster.
#
# Polls /api/status and re-issues /api/play when the controller is stuck in
# the "error" state (a lost connection it could not recover on its own).
# Deliberately ignores "disconnected" (a clean user Stop) and "paused" so the
# watchdog never fights an intentional action — it only resurrects a session
# the network killed.
set -u

APP="${APP_URL:-http://app:8080}"
INTERVAL="${INTERVAL:-30}"
FALLBACK_IP="${FALLBACK_SPEAKER_IP:-}"
: "${WN_USER:?WN_USER required}"
: "${WN_PASS:?WN_PASS required}"

log() { echo "[watchdog] $(date -u '+%Y-%m-%dT%H:%M:%SZ') $*"; }

errstreak=0
log "starting; polling $APP every ${INTERVAL}s (re-play on sustained state=error)"

while :; do
	sleep "$INTERVAL"

	status="$(curl -fsS -m 8 -u "$WN_USER:$WN_PASS" "$APP/api/status" 2>/dev/null)" || {
		log "status fetch failed"
		errstreak=0
		continue
	}

	state="$(printf '%s' "$status" | sed -n 's/.*"state":"\([a-z]*\)".*/\1/p')"
	if [ "$state" != "error" ]; then
		errstreak=0
		continue
	fi

	errstreak=$((errstreak + 1))
	log "state=error (streak=$errstreak)"

	# Require two consecutive error reads (~2*INTERVAL) before acting so we
	# don't race the in-process self-heal, which recovers faster than this.
	[ "$errstreak" -ge 2 ] || continue

	ip="$(printf '%s' "$status" | sed -n 's/.*"speaker_ip":"\([0-9.]*\)".*/\1/p')"
	[ -n "$ip" ] || ip="$FALLBACK_IP"
	if [ -z "$ip" ]; then
		log "state=error but no speaker_ip and no FALLBACK_SPEAKER_IP; cannot recover"
		continue
	fi

	log "re-playing on $ip"
	if curl -fsS -m 12 -u "$WN_USER:$WN_PASS" -H 'Content-Type: application/json' \
		-d "{\"speaker_ip\":\"$ip\"}" "$APP/api/play" >/dev/null 2>&1; then
		log "re-play sent"
		errstreak=0
	else
		log "re-play failed"
	fi
done
