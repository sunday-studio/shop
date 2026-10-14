#!/usr/bin/env bash
# Deploy and operate the hosted stack on the server. Run it as the deploy user from this folder.
# Every compose call goes through compose.sh (project shop-hosted); never call `docker compose` here.
#
#   ./deploy.sh preflight               check .env, secrets, tag, DNS, memory
#   ./deploy.sh first                   first deploy: pull, start, wait healthy, first backup + off-site copy, smoke test
#   ./deploy.sh update <tag>            snapshot first, then switch to image tag <tag>, wait healthy, smoke test
#   ./deploy.sh rollback [--restore-db] go back to the previous tag; --restore-db also restores the pre-update snapshot
#   ./deploy.sh restore [--from-offsite] [--snapshot NAME|latest] --yes     restore the database (see RUNBOOK.md)
#   ./deploy.sh smoke [url]             healthz + server-info checks
#   ./deploy.sh status | logs [service] | backup-now | gen-offsite
#
# Environment (for tests): HOSTED_COMPOSE (wrapper path), HOSTED_ENV_FILE, HOSTED_TEST=1 (skips DNS and file-mode checks).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE="${HOSTED_COMPOSE:-$HERE/compose.sh}"
ENV_FILE="${HOSTED_ENV_FILE:-$HERE/.env}"
export HOSTED_ENV_FILE="$ENV_FILE"
STATE_DIR="${HOSTED_STATE_DIR:-$HERE/.deploy}"
WAIT_TIMEOUT="${HOSTED_WAIT_TIMEOUT:-600}"
mkdir -p "$STATE_DIR"

say()  { printf '\n== %s\n' "$*"; }
die()  { echo "deploy: $*" >&2; exit 1; }
c()    { "$COMPOSE" "$@"; }
envget() { sed -n "s/^$1=\([^ #]*\).*/\1/p" "$ENV_FILE" | head -n 1; }
envset() { # envset KEY VALUE: rewrite one line of the env file atomically, keep the mode
  local tmp; tmp=$(mktemp "$ENV_FILE.XXXXXX")
  if grep -q "^$1=" "$ENV_FILE"; then sed "s|^$1=.*|$1=$2|" "$ENV_FILE" >"$tmp"; else { cat "$ENV_FILE"; echo "$1=$2"; } >"$tmp"; fi
  chmod --reference="$ENV_FILE" "$tmp" 2>/dev/null || chmod 600 "$tmp"
  mv "$tmp" "$ENV_FILE"
}
logline() { echo "$(date -u +%FT%TZ) $*" >>"$STATE_DIR/history.log"; }

preflight() {
  say "preflight"
  local err=0
  bad()  { echo "  FAIL  $*"; err=1; }
  warn() { echo "  warn  $*"; }
  good() { echo "  ok    $*"; }
  command -v docker >/dev/null || bad "docker is not installed (run bootstrap.sh)"
  [ -f "$ENV_FILE" ] || { bad "$ENV_FILE missing: cp .env.example .env, fill it in, chmod 600 .env"; return 1; }
  if [ "${HOSTED_TEST:-0}" != 1 ]; then
    local mode; mode=$(stat -c %a "$ENV_FILE" 2>/dev/null || stat -f %Lp "$ENV_FILE")
    [ "$mode" = 600 ] && good ".env mode 600" || bad ".env mode is $mode, run: chmod 600 $ENV_FILE"
  fi
  if grep -q 'CHANGE_ME' "$ENV_FILE"; then bad ".env still contains CHANGE_ME"; fi
  local tag domain remote
  tag=$(envget SHOP_API_TAG); domain=$(envget SHOP_DOMAIN); remote=$(envget OFFSITE_REMOTE)
  case "$tag" in ""|latest|CHANGE_ME*) bad "SHOP_API_TAG must be a version tag, not '$tag'" ;; *) good "image tag $tag" ;; esac
  [ -n "$domain" ] && [ "$domain" != "shop.example.com" ] && good "domain $domain" || bad "SHOP_DOMAIN is not set to your domain"
  [ -n "$(envget ACME_EMAIL)" ] || bad "ACME_EMAIL is not set"
  if [ -z "$remote" ]; then bad "OFFSITE_REMOTE is empty: the off-site copy is mandatory for the hosted stack"; else good "off-site remote $remote"; fi
  if [ "${HOSTED_TEST:-0}" != 1 ]; then
    if [ -f "$HERE/offsite.env" ]; then
      local m; m=$(stat -c %a "$HERE/offsite.env" 2>/dev/null || stat -f %Lp "$HERE/offsite.env")
      [ "$m" = 600 ] && good "offsite.env mode 600" || bad "offsite.env mode is $m, run: chmod 600 offsite.env"
      grep -q 'CHANGE_ME' "$HERE/offsite.env" && bad "offsite.env still contains CHANGE_ME"
      grep -q '_TYPE=crypt' "$HERE/offsite.env" && good "offsite.env defines a crypt remote" || bad "offsite.env has no crypt remote: backups must be encrypted"
    else
      bad "offsite.env missing (see offsite.env.example; ./deploy.sh gen-offsite helps)"
    fi
    if command -v getent >/dev/null && [ -n "$domain" ]; then
      getent hosts "$domain" >/dev/null && good "DNS resolves for $domain" || warn "no DNS answer for $domain yet: Caddy cannot get a certificate until it points here"
    fi
    local kb; kb=$(awk '/MemTotal/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)
    [ "$kb" -ge 3800000 ] && good "memory $((kb/1024)) MB" || warn "less than 4 GB RAM ($((kb/1024)) MB): OCR needs a 3 GB limit; see CAPACITY.md"
    local free; free=$(df -Pk / | awk 'NR==2 {print int($4/1024/1024)}')
    [ "$free" -ge 10 ] && good "free disk ${free} GB" || warn "free disk only ${free} GB"
  fi
  c config -q >/dev/null 2>"$STATE_DIR/config.err" && good "compose file renders" || { bad "compose config failed: $(head -n 3 "$STATE_DIR/config.err")"; }
  [ "$err" = 0 ] || die "preflight failed"
  return 0
}

wait_healthy() { # wait until every service with a healthcheck is healthy
  local start out waiting; start=$(date +%s)
  echo "  waiting up to ${WAIT_TIMEOUT}s for healthy services (OCR needs about two minutes to load)"
  while :; do
    out=$(c ps -a --format '{{.Service}}|{{.Health}}|{{.State}}' 2>/dev/null || true)
    waiting=$(printf '%s\n' "$out" | awk -F'|' '$2=="starting"||$2=="unhealthy"||$3=="restarting"||$3=="created"||$3=="dead" {printf "%s ", $1}')
    if [ -n "$out" ] && [ -z "$waiting" ] && printf '%s\n' "$out" | grep -q '^api|'; then
      printf '%s\n' "$out" | sed 's/^/    /'; return 0
    fi
    if [ $(( $(date +%s) - start )) -ge "$WAIT_TIMEOUT" ]; then
      printf '%s\n' "$out" | sed 's/^/    /'; echo "  still waiting for: $waiting"; return 1
    fi
    sleep 5
  done
}

base_url() { if [ -n "${HOSTED_BASE_URL:-}" ]; then echo "$HOSTED_BASE_URL"; else echo "https://$(envget SHOP_DOMAIN)"; fi; }
smoke() { "$HERE/smoke.sh" "${1:-$(base_url)}"; }

latest_snapshot() { # newest file in the backup volume's db folder
  c run --rm --no-deps -T --entrypoint ls offsite -1 /backup/db 2>/dev/null | grep '^shop-.*\.db$' | sort | tail -n 1 || true
}
backup_now() {
  say "backup now"
  c run --rm --no-deps -T backup backup
  # Copy it off-site right away and require that to work.
  c run --rm --no-deps -T offsite once
}

cmd="${1:-help}"; shift || true
case "$cmd" in
  preflight) preflight ;;

  first)
    preflight
    say "pull images"; c pull
    say "start"; c up -d --remove-orphans
    wait_healthy || die "stack did not become healthy; see: ./compose.sh logs --tail 100"
    logline "first deploy tag=$(envget SHOP_API_TAG)"
    backup_now || die "first backup or off-site copy failed; fix before relying on this server (see RUNBOOK.md)"
    smoke || die "smoke test failed"
    echo
    echo "Deployed. In single mode create the pairing password now: ./compose.sh exec api shop password generate"
    ;;

  update)
    new="${1:-}"; [ -n "$new" ] || die "usage: deploy.sh update <tag>"
    case "$new" in latest|"") die "pin a version tag, not '$new'" ;; esac
    old=$(envget SHOP_API_TAG)
    [ "$old" != "$new" ] || echo "note: already on $new, continuing (re-pull and re-check)"
    preflight
    say "pre-update snapshot (database + off-site copy)"
    backup_now || die "no fresh backup, refusing to update. Fix the backup first."
    snap=$(latest_snapshot)
    [ -n "$snap" ] || die "could not find the snapshot just taken"
    echo "$old" >"$STATE_DIR/previous-tag"; echo "$snap" >"$STATE_DIR/pre-update-snapshot"
    logline "update $old -> $new snapshot=$snap"
    envset SHOP_API_TAG "$new"
    say "pull $new"; c pull api backup || { envset SHOP_API_TAG "$old"; die "pull failed, tag restored to $old"; }
    say "restart on $new"; c up -d --remove-orphans
    if wait_healthy && smoke; then
      logline "update ok $new"
      echo "Updated $old -> $new. Pre-update snapshot: $snap. To go back: ./deploy.sh rollback"
    else
      logline "update FAILED $new"
      die "update to $new is not healthy. Run ./deploy.sh rollback (image only), or ./deploy.sh rollback --restore-db if the new version already changed the database."
    fi
    ;;

  rollback)
    restore_db=0; [ "${1:-}" = "--restore-db" ] && restore_db=1
    prev=$(cat "$STATE_DIR/previous-tag" 2>/dev/null || true); [ -n "$prev" ] || die "no previous tag recorded in $STATE_DIR/previous-tag"
    cur=$(envget SHOP_API_TAG)
    say "rollback $cur -> $prev (restore database: $restore_db)"
    logline "rollback $cur -> $prev restore_db=$restore_db"
    if [ "$restore_db" = 1 ]; then
      snap=$(cat "$STATE_DIR/pre-update-snapshot" 2>/dev/null || true); [ -n "$snap" ] || die "no pre-update snapshot recorded"
      c stop api backup monitor caddy
      envset SHOP_API_TAG "$prev"
      c pull api backup
      c run --rm --no-deps -T backup restore -yes -snapshot "$snap"
    else
      envset SHOP_API_TAG "$prev"
      c pull api backup
    fi
    c up -d --remove-orphans
    wait_healthy && smoke || die "still not healthy after rollback; see RUNBOOK.md 'Rollback' and 'Restore'"
    echo "Rolled back to $prev."
    ;;

  restore)
    from_off=0; snap="latest"; yes=0
    while [ $# -gt 0 ]; do
      case "$1" in
        --from-offsite) from_off=1 ;;
        --snapshot) snap="${2:-}"; shift ;;
        --yes) yes=1 ;;
        *) die "unknown option $1" ;;
      esac; shift
    done
    [ "$yes" = 1 ] || die "restore replaces the live database. Re-run with --yes when you are sure."
    say "restore snapshot=$snap from_offsite=$from_off"
    logline "restore snapshot=$snap from_offsite=$from_off"
    c stop api backup monitor caddy || true
    if [ "$from_off" = 1 ]; then c run --rm --no-deps -T offsite pull; fi
    c run --rm --no-deps -T backup restore -yes -snapshot "$snap"
    c up -d --remove-orphans
    wait_healthy && smoke || die "restored, but the stack is not healthy"
    echo "Restored. /healthz shows no off-site status until the next backup; run ./deploy.sh backup-now."
    ;;

  smoke) smoke "${1:-}" ;;
  status) c ps; echo; cat "$STATE_DIR/history.log" 2>/dev/null | tail -n 10 ;;
  logs) c logs --tail 100 "$@" ;;
  backup-now) backup_now ;;

  gen-offsite)
    # Prints an offsite.env with fresh, obscured crypt password and salt. Storage credentials are left to fill in.
    command -v docker >/dev/null || die "docker needed"
    p=$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n'); s=$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')
    op=$(docker run --rm rclone/rclone:1.68.2 obscure "$p"); os=$(docker run --rm rclone/rclone:1.68.2 obscure "$s")
    sed -e "s|^RCLONE_CONFIG_SHOPCRYPT_PASSWORD=.*|RCLONE_CONFIG_SHOPCRYPT_PASSWORD=$op|" \
        -e "s|^RCLONE_CONFIG_SHOPCRYPT_PASSWORD2=.*|RCLONE_CONFIG_SHOPCRYPT_PASSWORD2=$os|" "$HERE/offsite.env.example" >"$HERE/offsite.env.new"
    chmod 600 "$HERE/offsite.env.new"
    echo "Wrote $HERE/offsite.env.new (mode 600). Fill in the STORE endpoint, bucket and keys, then mv it to offsite.env."
    echo "SAVE THESE TWO LINES OFF THIS SERVER NOW (password manager). Without them the backups are unreadable:"
    echo "  crypt password (plain): $p"
    echo "  crypt salt     (plain): $s"
    ;;

  help|*) sed -n '2,15p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' ;;
esac
