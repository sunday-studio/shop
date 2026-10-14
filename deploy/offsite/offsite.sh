#!/bin/sh
# Optional off-site copy of the Shop backup folder, using rclone.
#
# rclone talks to S3-compatible stores, Backblaze B2, SFTP, WebDAV, Google Drive, Dropbox,
# a plain folder or a NAS mount. Nothing here depends on one of them: the destination is
# whatever OFFSITE_REMOTE names, and rclone's own configuration (a mounted rclone.conf or
# RCLONE_CONFIG_* environment variables) says how to reach it.
#
# Commands:
#   loop   (default) copy after every successful backup, retry after a failure
#   once   copy now and exit non-zero if it failed
#   pull   copy the off-site copy back into the backup folder (for a restore)
#   list   show what is at the destination
#
# Order matters: images first, then snapshots, then the status file. A snapshot at the
# destination therefore never points at images that have not arrived yet.
#
# Settings (environment):
#   OFFSITE_REMOTE          rclone destination, e.g. b2crypt:shop or sftp-nas:/volume1/shop. Required.
#   OFFSITE_LABEL           name shown in /healthz (default: the part before the first colon). No secrets.
#   OFFSITE_PRUNE           on (default): old snapshots are removed at the destination as locally.
#                           off: snapshots are only ever added at the destination.
#   OFFSITE_RCLONE_FLAGS    extra rclone flags, e.g. "--bwlimit 2M"
#   OFFSITE_RETRY_MINUTES   wait before retrying after a failure (default 30)
#   OFFSITE_SRC_DIR         backup folder (default /backup)
#   OFFSITE_STATUS_FILE     status file read by the API (default /data/offsite_status.json)

set -u

REMOTE="${OFFSITE_REMOTE:-}"
SRC="${OFFSITE_SRC_DIR:-/backup}"
STATUS="${OFFSITE_STATUS_FILE:-/data/offsite_status.json}"
PRUNE="${OFFSITE_PRUNE:-on}"
FLAGS="${OFFSITE_RCLONE_FLAGS:-}"
RETRY_MIN="${OFFSITE_RETRY_MINUTES:-30}"
LABEL="${OFFSITE_LABEL:-}"
if [ -z "$LABEL" ]; then
  LABEL="${REMOTE%%:*}"
  [ -n "$LABEL" ] || LABEL="off-site"
fi

log() { echo "offsite: $*"; }

# join "<remote>" "<sub>" -> remote/sub, remote:sub, or remote: + sub
join() {
  case "$1" in
    *:) printf '%s%s' "$1" "$2" ;;
    */) printf '%s%s' "$1" "$2" ;;
    *) printf '%s/%s' "$1" "$2" ;;
  esac
}

# json_escape: drop quotes, backslashes and control characters, cut to 300 characters.
json_clean() { printf '%s' "$1" | tr -d '"\\' | tr '\n\r\t' '   ' | cut -c1-300; }

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

last_ok_from_file() {
  [ -f "$STATUS" ] || return 0
  sed -n 's/.*"last_ok_at": *"\([^"]*\)".*/\1/p' "$STATUS" | head -n 1
}

write_status() { # ok(true|false) at last_ok_at snapshot error duration_ms last_ok_unix
  dir=$(dirname "$STATUS")
  [ -d "$dir" ] || return 0
  tmp="$STATUS.tmp"
  {
    printf '{\n'
    printf '  "ok": %s,\n' "$1"
    printf '  "at": "%s",\n' "$2"
    [ -n "$3" ] && printf '  "last_ok_at": "%s",\n' "$3"
    [ -n "${7:-}" ] && printf '  "last_ok_unix": %s,\n' "$7"
    printf '  "destination": "%s",\n' "$(json_clean "$LABEL")"
    [ -n "$4" ] && printf '  "snapshot": "%s",\n' "$(json_clean "$4")"
    [ -n "$5" ] && printf '  "error": "%s",\n' "$(json_clean "$5")"
    printf '  "duration_ms": %s\n' "$6"
    printf '}\n'
  } >"$tmp" && mv "$tmp" "$STATUS"
}

# sync_once: returns 0 on success; sets ERR on failure.
ERR=""
SNAP=""
sync_once() {
  ERR=""
  SNAP=""
  if [ -z "$REMOTE" ]; then ERR="OFFSITE_REMOTE is not set"; return 1; fi
  if [ ! -d "$SRC/db" ]; then ERR="no backup to copy: $SRC/db not found (has a backup run?)"; return 1; fi

  # shellcheck disable=SC2086
  if [ -d "$SRC/images" ]; then
    out=$(rclone copy "$SRC/images" "$(join "$REMOTE" images)" --ignore-existing --exclude '*.partial' --exclude '.*' $FLAGS 2>&1) \
      || { ERR="images: $(printf '%s' "$out" | tail -n 1)"; return 1; }
  fi
  if [ "$PRUNE" = "off" ]; then verb=copy; else verb=sync; fi
  # shellcheck disable=SC2086
  out=$(rclone "$verb" "$SRC/db" "$(join "$REMOTE" db)" --exclude '*.partial' $FLAGS 2>&1) \
    || { ERR="snapshots: $(printf '%s' "$out" | tail -n 1)"; return 1; }
  # Every local snapshot must now exist at the destination with the same size.
  # shellcheck disable=SC2086
  out=$(rclone check "$SRC/db" "$(join "$REMOTE" db)" --one-way --size-only --exclude '*.partial' $FLAGS 2>&1) \
    || { ERR="check: $(printf '%s' "$out" | tail -n 1)"; return 1; }
  if [ -d "$SRC/households" ]; then
    # Hosted layout: one folder per household. Images are append-only (copied once), snapshots follow
    # the prune setting; the last_backup.json below is copied last, so a complete set is recognisable.
    # shellcheck disable=SC2086
    out=$(rclone copy "$SRC/households" "$(join "$REMOTE" households)" --ignore-existing --exclude '*.partial' --exclude '.*' --exclude '/*/db/**' $FLAGS 2>&1) \
      || { ERR="household images: $(printf '%s' "$out" | tail -n 1)"; return 1; }
    # shellcheck disable=SC2086
    out=$(rclone "$verb" "$SRC/households" "$(join "$REMOTE" households)" --exclude '*.partial' --exclude '/*/images/**' $FLAGS 2>&1) \
      || { ERR="household snapshots: $(printf '%s' "$out" | tail -n 1)"; return 1; }
    # shellcheck disable=SC2086
    out=$(rclone check "$SRC/households" "$(join "$REMOTE" households)" --one-way --size-only --exclude '*.partial' $FLAGS 2>&1) \
      || { ERR="household check: $(printf '%s' "$out" | tail -n 1)"; return 1; }
  fi
  if [ -f "$SRC/last_backup.json" ]; then
    # shellcheck disable=SC2086
    out=$(rclone copyto "$SRC/last_backup.json" "$(join "$REMOTE" last_backup.json)" $FLAGS 2>&1) \
      || { ERR="status file: $(printf '%s' "$out" | tail -n 1)"; return 1; }
  fi
  SNAP=$(ls "$SRC/db" 2>/dev/null | grep '^shop-.*\.db$' | sort | tail -n 1)
  return 0
}

record() { # runs sync_once and writes the status file; returns its result
  start=$(date -u +%s)
  prev_ok=$(last_ok_from_file)
  at=$(now)
  if sync_once; then
    ms=$(( ($(date -u +%s) - start) * 1000 ))
    write_status true "$at" "$at" "$SNAP" "" "$ms" "$(date -u +%s)"
    log "synced $SNAP to $LABEL"
    return 0
  fi
  ms=$(( ($(date -u +%s) - start) * 1000 ))
  write_status false "$at" "$prev_ok" "" "$ERR" "$ms" "$(sed -n 's/.*"last_ok_unix": *\([0-9]*\).*/\1/p' "$STATUS" 2>/dev/null | head -n 1)"
  log "FAILED: $ERR"
  return 1
}

cmd="${1:-loop}"
case "$cmd" in
  once)
    record
    exit $?
    ;;
  list)
    [ -n "$REMOTE" ] || { echo "OFFSITE_REMOTE is not set" >&2; exit 2; }
    # shellcheck disable=SC2086
    exec rclone lsf --recursive "$REMOTE" $FLAGS
    ;;
  pull)
    [ -n "$REMOTE" ] || { echo "OFFSITE_REMOTE is not set" >&2; exit 2; }
    mkdir -p "$SRC" || exit 1
    # shellcheck disable=SC2086
    rclone copy "$(join "$REMOTE" db)" "$SRC/db" --exclude '*.partial' $FLAGS || exit 1
    # (a hosted copy has no top-level images/: its images live under households/)
    # shellcheck disable=SC2086
    if [ -n "$(rclone lsf "$(join "$REMOTE" images)" --max-depth 1 $FLAGS 2>/dev/null | head -n 1)" ]; then
      # shellcheck disable=SC2086
      rclone copy "$(join "$REMOTE" images)" "$SRC/images" --ignore-existing $FLAGS || exit 1
    fi
    # Hosted layout: the household folders come back too (nothing there for a single-household copy).
    if [ -n "$(rclone lsf "$(join "$REMOTE" households)" --max-depth 1 $FLAGS 2>/dev/null | head -n 1)" ]; then
      # shellcheck disable=SC2086
      rclone copy "$(join "$REMOTE" households)" "$SRC/households" --exclude '*.partial' $FLAGS || exit 1
    fi
    n=$(ls "$SRC/db" 2>/dev/null | grep -c '^shop-.*\.db$')
    [ "$n" -gt 0 ] || { echo "offsite: no snapshots came back from $LABEL" >&2; exit 1; }
    log "pulled $n snapshot(s) from $LABEL into $SRC; now run the restore (docs/restore.md)"
    exit 0
    ;;
  loop) ;;
  *) echo "usage: offsite.sh [loop|once|pull|list]" >&2; exit 2 ;;
esac

if [ -z "$REMOTE" ]; then
  log "OFFSITE_REMOTE is not set, so nothing is copied off-site. Set it and restart this service."
  # Idle instead of exiting, so "restart: unless-stopped" does not spin.
  while true; do sleep 3600; done
fi

log "watching $SRC; destination $LABEL (prune $PRUNE)"
synced_marker=""
failed_at=0
failed=0
while true; do
  marker=""
  [ -f "$SRC/last_backup.json" ] && marker=$(cksum <"$SRC/last_backup.json")
  due=0
  if [ -n "$marker" ] && [ "$marker" != "$synced_marker" ]; then due=1; fi
  if [ "$failed" = 1 ] && [ $(( $(date -u +%s) - failed_at )) -ge $(( RETRY_MIN * 60 )) ]; then due=1; fi
  if [ "$due" = 1 ]; then
    if record; then
      synced_marker="$marker"
      failed=0
    else
      failed=1
      failed_at=$(date -u +%s)
      synced_marker="$marker"   # retry on the timer, not every minute
    fi
  fi
  sleep 60
done
