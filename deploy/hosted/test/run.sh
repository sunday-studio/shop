#!/usr/bin/env bash
# `make hosted-kit-test`: runs the hosted kit end to end in an isolated compose project
# (shop-hosted-test, port 127.0.0.1:18490, throwaway volumes, http-only Caddy, no public DNS).
# Every compose call goes through test/compose.sh, which refuses any other project name.
# Proves: static checks, bootstrap in a throwaway Debian container, stack healthy, receipt upload and
# read (single mode), encrypted off-site backup, alerts, update with snapshot, rollback, restore
# from the off-site copy, teardown. Never touches the live stack (project shop, port 8080).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KIT="$(dirname "$HERE")"
ROOT="$(cd "$KIT/../.." && pwd)"
PROJECT="shop-hosted-test"
PORT=18490
SINK_PORT=18491
BASE="http://127.0.0.1:$PORT"
TMP="$(mktemp -d)"
TC="$HERE/compose.sh"

pass=0; failn=0
ok()   { echo "  PASS  $*"; pass=$((pass+1)); }
fail() { echo "  FAIL  $*"; failn=$((failn+1)); }
check() { # check "description" command...
  local d="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$d"; else fail "$d"; fi
}
section() { printf '\n== %s\n' "$*"; }
c() { "$TC" "$@"; }

echo "hosted-kit-test: compose project is '$PROJECT' (the only name test/compose.sh accepts)"
[ "$PROJECT" = shop-hosted-test ] || { echo "wrong project name"; exit 1; }

# The live stack: remember it, compare at the end. Read-only commands only.
live_id_before="$(docker ps -q --filter 'name=^shop-api-1$' || true)"
live_vol_before="$(docker volume ls -q --filter 'name=^shop_shop-data$' || true)"
echo "live stack before: api container '${live_id_before:-none}', volume '${live_vol_before:-none}' (not touched)"

SINK_PID=""
cleanup() {
  rc=$?
  set +e
  if [ "${HOSTED_KEEP:-0}" = 1 ]; then
    echo "HOSTED_KEEP=1: leaving project $PROJECT running (remove with: test/compose.sh down -v --remove-orphans)"
  else
    section "teardown"
    echo "compose project to remove: $PROJECT (down -v --remove-orphans)"
    HOSTED_ENV_FILE="$TMP/test.env" c down -v --remove-orphans >/dev/null 2>&1
    docker image rm -f shop-hosted-test-api:t1 shop-hosted-test-api:t2 >/dev/null 2>&1
    left=$(docker ps -aq --filter "label=com.docker.compose.project=$PROJECT" | wc -l | tr -d ' ')
    vols=$(docker volume ls -q --filter "label=com.docker.compose.project=$PROJECT" | wc -l | tr -d ' ')
    if [ "$left" = 0 ] && [ "$vols" = 0 ]; then ok "torn down: no containers or volumes left in $PROJECT"; else fail "leftovers: $left containers, $vols volumes"; fi
  fi
  [ -z "$SINK_PID" ] || kill "$SINK_PID" 2>/dev/null
  live_id_after="$(docker ps -q --filter 'name=^shop-api-1$' || true)"
  live_vol_after="$(docker volume ls -q --filter 'name=^shop_shop-data$' || true)"
  if [ "$live_id_before" = "$live_id_after" ] && [ "$live_vol_before" = "$live_vol_after" ]; then ok "live stack untouched (same api container, volume still there)"; else fail "LIVE STACK CHANGED"; fi
  rm -rf "$TMP"
  echo
  echo "hosted-kit-test: $pass passed, $failn failed"
  [ "$rc" = 0 ] && [ "$failn" = 0 ] || exit 1
}
trap cleanup EXIT

section "static checks"
if command -v shellcheck >/dev/null; then
  check "shellcheck (warnings and errors) on all kit scripts" shellcheck -S warning -x \
    "$KIT"/*.sh "$KIT"/monitor/*.sh "$HERE"/*.sh
else echo "  skip  shellcheck not installed"; fi

cp "$HERE/test.env" "$TMP/test.env"
export HOSTED_ENV_FILE="$TMP/test.env" HOSTED_COMPOSE="$TC" HOSTED_STATE_DIR="$TMP/state" HOSTED_TEST=1
export HOSTED_BASE_URL="$BASE" HOSTED_WAIT_TIMEOUT=420 SHOP_VERSION=hosted-kit-test
DEPLOY="$KIT/deploy.sh"

cfg="$TMP/config.json"
c config --format json >"$cfg"
check "compose file renders; project name is $PROJECT" python3 -c "import json,sys; d=json.load(open('$cfg')); assert d['name']=='$PROJECT', d['name']"
check "only caddy publishes a port (api, ocr, backup, offsite, monitor are internal)" python3 - "$cfg" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
pub = {n: s.get("ports") for n, s in d["services"].items() if s.get("ports")}
assert list(pub) == ["caddy"], pub
PY
check "ocr has a 3 GiB memory limit; every long-running service has log rotation" python3 - "$cfg" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))["services"]
assert str(d["ocr"]["mem_limit"]) in ("3221225472", "3g"), d["ocr"].get("mem_limit")
for n, s in d.items():
    o = s.get("logging", {}).get("options", {})
    assert o.get("max-size") and o.get("max-file"), n
    assert s.get("mem_limit"), n + " has no memory limit"
PY
check "SHOP_MODE reaches the api container" python3 -c "import json; d=json.load(open('$cfg')); assert d['services']['api']['environment']['SHOP_MODE']=='single'"
grep -v '^SHOP_API_TAG=' "$TMP/test.env" >"$TMP/no-tag.env"
check "production compose refuses a missing image tag (no default to latest)" bash -c "! HOSTED_ENV_FILE='$TMP/no-tag.env' '$KIT/compose.sh' config -q"
check "production wrapper refuses another project and down -v" bash -c "
  ! COMPOSE_PROJECT_NAME=shop HOSTED_ENV_FILE='$TMP/test.env' '$KIT/compose.sh' ps 2>/dev/null &&
  ! HOSTED_ENV_FILE='$TMP/test.env' '$KIT/compose.sh' down -v 2>/dev/null &&
  ! HOSTED_ENV_FILE='$TMP/test.env' '$KIT/compose.sh' -p shop ps 2>/dev/null"
check "test wrapper refuses project shop" bash -c "! COMPOSE_PROJECT_NAME=shop '$TC' ps 2>/dev/null && ! '$TC' -p shop ps 2>/dev/null"

section "bootstrap.sh in a throwaway Debian container"
if "$HERE/bootstrap-container.sh" >"$TMP/bootstrap.log" 2>&1 && grep -q BOOTSTRAP-OK "$TMP/bootstrap.log"; then ok "bootstrap: dry run, real run, second run changes nothing, ufw 22/80/443 only, ssh hardened, docker installed from the official repo"; else tail -n 20 "$TMP/bootstrap.log"; fail "bootstrap container test"; fi

section "images"
docker image inspect shop-ocr:ppocrv5 >/dev/null 2>&1 || { echo "building shop-ocr:ppocrv5 (once, slow)"; docker build -t shop-ocr:ppocrv5 --build-arg OCR_ENGINE=ppocrv5 "$ROOT/ocr"; }
c build api >"$TMP/build.log" 2>&1 && ok "api image built from the working tree (shop-hosted-test-api:t1)" || { tail -n 20 "$TMP/build.log"; fail "api build"; exit 1; }
# An encrypted off-site remote with throwaway passwords (rclone wants the obscured form).
TEST_CRYPT_PASSWORD="$(docker run --rm rclone/rclone:1.68.2 obscure "kit-test-$RANDOM-password")"
TEST_CRYPT_PASSWORD2="$(docker run --rm rclone/rclone:1.68.2 obscure "kit-test-$RANDOM-salt")"
export TEST_CRYPT_PASSWORD TEST_CRYPT_PASSWORD2
python3 "$HERE/webhook_sink.py" "$SINK_PORT" "$TMP/webhook.log" & SINK_PID=$!
export TEST_WEBHOOK_URL="http://host.docker.internal:$SINK_PORT/hook"

section "first deploy (deploy.sh first)"
if "$DEPLOY" first >"$TMP/first.log" 2>&1; then ok "deploy.sh first: preflight, up, all healthy, first backup and encrypted off-site copy, smoke test"; else tail -n 40 "$TMP/first.log"; fail "deploy.sh first"; exit 1; fi
check "smoke test passes (healthz, server-info or SKIP, auth/status, 401 without token)" "$KIT/smoke.sh" "$BASE"
for s in caddy api ocr backup offsite monitor; do
  [ -n "$(c ps -q --status running "$s")" ] && ok "service $s is running" || fail "service $s is not running"
done
ocr_mem=$(docker inspect --format '{{.HostConfig.Memory}}' "$(c ps -q ocr)")
[ "$ocr_mem" = 3221225472 ] && ok "ocr container runs with the 3 GiB limit" || fail "ocr memory limit is $ocr_mem"
rot=$(docker inspect --format '{{.HostConfig.LogConfig.Config}}' "$(c ps -q api)")
case "$rot" in *max-size:10m*) ok "log rotation active ($rot)" ;; *) fail "log rotation: $rot" ;; esac

section "single mode: pair, upload a receipt, read it back"
pw="$(c exec -T api /usr/local/bin/shop password generate | tail -n 1 | tr -d '\r')"
tok="$(curl -fsS -X POST "$BASE/v1/auth/pair" -H 'Content-Type: application/json' -d "{\"password\":\"$pw\",\"device_name\":\"hosted-kit-test\"}" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
[ -n "$tok" ] && ok "paired a device through the front (token received)" || { fail "pairing"; exit 1; }
docker run --rm -i shop-ocr:ppocrv5 python - <"$HERE/mkreceipt.py" >"$TMP/receipt.png"
[ -s "$TMP/receipt.png" ] && ok "generated an invented receipt image ($(wc -c <"$TMP/receipt.png" | tr -d ' ') bytes)" || fail "receipt image"
code=$(curl -sS -o "$TMP/up.json" -w '%{http_code}' -X POST "$BASE/v1/receipts" -H "Authorization: Bearer $tok" -F "image=@$TMP/receipt.png;type=image/png")
rid="$(sed -n 's/.*"id":\([0-9]*\).*/\1/p' "$TMP/up.json" | head -n 1)"
[ "$code" = 202 ] && [ -n "$rid" ] && ok "upload accepted (202), receipt id $rid" || { cat "$TMP/up.json"; fail "upload http=$code"; exit 1; }
status=""
for _ in $(seq 1 90); do
  status="$(curl -fsS "$BASE/v1/receipts/$rid" -H "Authorization: Bearer $tok" | sed -n 's/.*"status":"\([a-z_]*\)".*/\1/p' | head -n 1)"
  case "$status" in pending|processing|"") sleep 3 ;; *) break ;; esac
done
case "$status" in ok|needs_review) ok "OCR read the receipt (status $status)" ;; *) fail "receipt status after waiting: '$status'" ;; esac
code=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/v1/receipts/$rid/image" -H "Authorization: Bearer $tok")
[ "$code" = 200 ] && ok "receipt image served (200)" || fail "image http=$code"

section "backup profile: snapshot, encrypted off-site copy, health"
"$DEPLOY" backup-now >"$TMP/backup.log" 2>&1 && ok "backup-now: snapshot written and copied off-site" || { tail -n 20 "$TMP/backup.log"; fail "backup-now"; }
hz="$(curl -sS "$BASE/healthz" | tr -d ' \n')"
printf '%s' "$hz" | grep -q '"backup":{[^}]*"ok":true' && ok "/healthz backup check ok (includes the off-site status)" || fail "healthz backup: $hz"
store="$(c run --rm --no-deps -T --entrypoint sh offsite -c 'find /offsite-store -type f | head -n 50')"
[ -n "$store" ] && ok "off-site store has files" || fail "off-site store is empty"
if printf '%s' "$store" | grep -Eq 'shop-[0-9]{8}|\.db$|last_backup|receipts'; then fail "off-site file names are NOT encrypted"; else ok "off-site file names are encrypted (no plain snapshot or image names)"; fi

section "monitoring: alerts reach the webhook"
if c run --rm --no-deps -T monitor once >"$TMP/m1.log" 2>&1; then ok "monitor once: healthy stack, exit 0"; else tail -n 5 "$TMP/m1.log"; fail "monitor on a healthy stack"; fi
c run --rm --no-deps -T -e DISK_ALERT_PCT=0 monitor once >"$TMP/m2.log" 2>&1 && fail "disk alert did not trip" || ok "disk above threshold makes the monitor exit 1"
c run --rm --no-deps -T -e QUEUE_ALERT_PENDING=-1 -e QUEUE_ALERT_CHECKS=1 monitor once >"$TMP/m3.log" 2>&1 && fail "queue alert did not trip" || ok "OCR queue backlog makes the monitor exit 1"
c run --rm --no-deps -T -e MONITOR_HEALTH_URL="http://host.docker.internal:$SINK_PORT/healthz" monitor once >"$TMP/m4.log" 2>&1 && fail "backup alert did not trip" || ok "a failing backup check makes the monitor exit 1"
sleep 1
for k in "ALERT disk" "ALERT queue" "ALERT backup"; do
  grep -q "$k" "$TMP/webhook.log" 2>/dev/null && ok "webhook received: $k" || fail "webhook missing: $k"
done
c run --rm --no-deps -T monitor once >/dev/null 2>&1 || true
sleep 1
grep -q "recovered disk" "$TMP/webhook.log" && ok "webhook received a recovery message" || fail "no recovery message"
"$KIT/monitor/uptime-check.sh" "$BASE" >"$TMP/up.txt" && grep -q '^OK' "$TMP/up.txt" && ok "uptime-check.sh: OK on the healthy stack" || fail "uptime-check ok case"
"$KIT/monitor/uptime-check.sh" "http://127.0.0.1:9" >"$TMP/up2.txt"; [ $? = 2 ] && ok "uptime-check.sh: exit 2 when nothing answers" || fail "uptime-check bad case"

section "update with pre-update snapshot, then rollback"
docker tag shop-hosted-test-api:t1 shop-hosted-test-api:t2
if "$DEPLOY" update t2 >"$TMP/update.log" 2>&1; then ok "deploy.sh update t2 healthy"; else tail -n 30 "$TMP/update.log"; fail "update t2"; fi
[ -s "$TMP/state/pre-update-snapshot" ] && ok "pre-update snapshot recorded: $(cat "$TMP/state/pre-update-snapshot")" || fail "no snapshot recorded"
img="$(docker inspect --format '{{.Config.Image}}' "$(c ps -q api)")"
[ "$img" = "shop-hosted-test-api:t2" ] && ok "api now runs $img" || fail "api image is $img"
if "$DEPLOY" rollback >"$TMP/rollback.log" 2>&1; then ok "deploy.sh rollback healthy"; else tail -n 30 "$TMP/rollback.log"; fail "rollback"; fi
img="$(docker inspect --format '{{.Config.Image}}' "$(c ps -q api)")"
[ "$img" = "shop-hosted-test-api:t1" ] && ok "api is back on $img" || fail "after rollback api image is $img"

section "restore from the off-site copy"
curl -fsS -X DELETE "$BASE/v1/receipts/$rid" -H "Authorization: Bearer $tok" >/dev/null && ok "deleted the receipt (simulated loss)" || fail "delete"
c run --rm --no-deps -T --entrypoint sh offsite -c 'rm -rf /backup/db /backup/images /backup/last_backup.json' && ok "wiped the local backup folder (simulated lost disk)"
"$DEPLOY" restore --from-offsite --yes >"$TMP/restore.log" 2>&1 && ok "deploy.sh restore --from-offsite healthy" || { tail -n 30 "$TMP/restore.log"; fail "restore"; }
code=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/v1/receipts/$rid" -H "Authorization: Bearer $tok")
[ "$code" = 200 ] && ok "the receipt is back after the restore (old token still works)" || fail "receipt after restore http=$code"
code=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/v1/receipts/$rid/image" -H "Authorization: Bearer $tok")
[ "$code" = 200 ] && ok "its image is back too" || fail "image after restore http=$code"
"$DEPLOY" smoke >/dev/null 2>&1 && ok "smoke test passes after restore" || fail "smoke after restore"
