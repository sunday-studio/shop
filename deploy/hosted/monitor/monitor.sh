#!/bin/sh
# Free monitoring for the hosted stack. POSIX sh + curl only; runs in the `monitor` compose
# service (curlimages/curl) and also by hand on any host that has curl.
#
#   monitor.sh loop    check every MONITOR_INTERVAL seconds, forever (the compose default)
#   monitor.sh once    one check; exit 0 when everything is fine, 1 when something is wrong
#
# What it watches (each is an independent alert with a recovery message):
#   api      /healthz does not answer or is not 200. The public /healthz of the hosted server only
#            says ok or degraded; the details are not shown to anyone but a local caller.
#   ocr      OCR engine not ready: read from MONITOR_OCR_URL (the OCR service's own /healthz) and,
#            when /healthz carries details (single-household server), from there
#   backup   the nightly backup set failed or is older than 36 h, or the encrypted off-site copy
#            failed or is older than 36 h. With MONITOR_BACKUP_DIR set (hosted) the status files
#            last_backup.json and offsite_status.json in that folder are read; otherwise the
#            "backup" check of /healthz is used
#   disk     DISK_PATH is at or above DISK_ALERT_PCT percent
#   queue    more than QUEUE_ALERT_PENDING receipt jobs queued/running for QUEUE_ALERT_CHECKS
#            checks in a row (OCR is falling behind)
#
# Notifications, any combination, none required (without any, alerts go to the log only):
#   ALERT_WEBHOOK_URL   POST. ALERT_WEBHOOK_FORMAT=json sends {"text":MSG,"content":MSG}
#                       (Slack, Discord, Mattermost, many others); =text sends MSG as the body
#                       (ntfy.sh topic URLs).
#   ALERT_EMAIL_TO + ALERT_EMAIL_FROM + SMTP_URL (+ SMTP_USER/SMTP_PASSWORD)  email via curl
#   HEALTHCHECKS_PING_URL  GET after every fully healthy check ("/fail" is appended on trouble
#                          unless the URL contains it): a dead-man's switch that also notices
#                          when this whole server or the monitor itself is gone.
#
# State (when an alert was sent, queue streak) lives in STATE_DIR.

set -u

HEALTH_URL="${MONITOR_HEALTH_URL:-http://api:8080/healthz}"
INTERVAL="${MONITOR_INTERVAL:-300}"
NAME="${MONITOR_NAME:-shop}"
DISK_PATH="${DISK_PATH:-/}"
DISK_PCT="${DISK_ALERT_PCT:-80}"
Q_PENDING="${QUEUE_ALERT_PENDING:-15}"
Q_CHECKS="${QUEUE_ALERT_CHECKS:-3}"
REPEAT_H="${ALERT_REPEAT_HOURS:-6}"
STATE="${STATE_DIR:-/state}"
WEBHOOK="${ALERT_WEBHOOK_URL:-}"
WEBHOOK_FMT="${ALERT_WEBHOOK_FORMAT:-json}"
MAIL_TO="${ALERT_EMAIL_TO:-}"
MAIL_FROM="${ALERT_EMAIL_FROM:-}"
SMTP="${SMTP_URL:-}"
PING="${HEALTHCHECKS_PING_URL:-}"
OCR_URL="${MONITOR_OCR_URL:-}"
BACKUP_DIR="${MONITOR_BACKUP_DIR:-}"
STALE_S=$(( 36 * 3600 ))

log() { echo "monitor: $*"; }
mkdir -p "$STATE" 2>/dev/null || STATE=/tmp/shop-monitor-state && mkdir -p "$STATE"

# json_str: make a value safe inside a JSON string.
json_str() { printf '%s' "$1" | tr -d '\000-\037"\\' | cut -c1-900; }

notify() { # notify <subject> <body>
  subject="[$NAME] $1"
  body="$2"
  sent=0
  if [ -n "$WEBHOOK" ]; then
    if [ "$WEBHOOK_FMT" = "text" ]; then
      curl -fsS -m 15 -H "Title: $subject" -d "$body" "$WEBHOOK" >/dev/null 2>&1 && sent=1 || log "webhook failed"
    else
      m=$(json_str "$subject: $body")
      curl -fsS -m 15 -H 'Content-Type: application/json' \
        -d "{\"text\":\"$m\",\"content\":\"$m\"}" "$WEBHOOK" >/dev/null 2>&1 && sent=1 || log "webhook failed"
    fi
  fi
  if [ -n "$MAIL_TO" ] && [ -n "$SMTP" ] && [ -n "$MAIL_FROM" ]; then
    mailfile="$STATE/mail.tmp"
    printf 'From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n' "$MAIL_FROM" "$MAIL_TO" "$subject" "$body" >"$mailfile"
    auth=""
    [ -n "${SMTP_USER:-}" ] && auth="--user ${SMTP_USER}:${SMTP_PASSWORD:-}"
    # shellcheck disable=SC2086
    curl -fsS -m 30 --ssl-reqd --mail-from "$MAIL_FROM" --mail-rcpt "$MAIL_TO" $auth -T "$mailfile" "$SMTP" >/dev/null 2>&1 \
      && sent=1 || log "email failed"
    rm -f "$mailfile"
  fi
  [ "$sent" = 1 ] || log "(no notification channel delivered) $subject: $body"
}

now() { date -u +%s; }

# raise <key> <message>: alert once, repeat every REPEAT_H hours while it stays wrong.
raise() {
  f="$STATE/alert.$1"
  FAILED=1
  if [ -f "$f" ]; then
    last=$(cat "$f" 2>/dev/null || echo 0)
    [ $(( $(now) - last )) -ge $(( REPEAT_H * 3600 )) ] || { log "still: $1: $2"; return 0; }
  fi
  log "ALERT $1: $2"
  notify "ALERT $1" "$2"
  now >"$f"
}
# clear <key>: send a recovery message if an alert was open.
clear() {
  f="$STATE/alert.$1"
  [ -f "$f" ] || return 0
  rm -f "$f"
  log "recovered $1"
  notify "recovered $1" "$1 is fine again"
}

# check_obj <flat-json> <name>: prints "true" or "false" for checks.<name>.ok, empty when absent.
check_ok() {
  obj=$(printf '%s' "$1" | sed -n "s/.*\"$2\":{\([^}]*\)}.*/\1/p")
  [ -n "$obj" ] || return 0
  case "$obj" in *'"ok":true'*) echo true ;; *) echo false ;; esac
}
check_detail() { printf '%s' "$1" | sed -n "s/.*\"$2\":{\([^}]*\)}.*/\1/p" | sed -n 's/.*"detail":"\([^"]*\)".*/\1/p'; }

# status_file_problem <flat-json-file> <label>: prints a problem description, or nothing when the
# file says ok and its time (at_unix / last_ok_unix) is less than 36 h old. A file that does not
# exist yet is only a problem once the monitor has been running for 2 hours (the first backup of a
# new deployment is taken right after the first start).
status_file_problem() {
  f="$1"; label="$2"; key="$3"
  [ -f "$STATE/first_seen" ] || now >"$STATE/first_seen"
  if [ ! -f "$f" ]; then
    [ $(( $(now) - $(cat "$STATE/first_seen" 2>/dev/null || now) )) -ge 7200 ] && echo "$label: no status file ($f)"
    return 0
  fi
  j=$(tr -d ' \n' <"$f")
  case "$j" in *'"ok":true'*) ;; *) echo "$label failing: $(printf '%s' "$j" | sed -n 's/.*"error":"\([^"]*\)".*/\1/p' | cut -c1-200)"; return 0 ;; esac
  t=$(printf '%s' "$j" | sed -n "s/.*\"$key\":\([0-9][0-9]*\).*/\1/p")
  if [ -n "$t" ] && [ $(( $(now) - t )) -gt "$STALE_S" ]; then echo "$label stale: last good one $(( ($(now) - t) / 3600 )) h ago"; fi
  return 0
}

run_once() {
  FAILED=0
  tmp="$STATE/healthz.json"
  rm -f "$tmp"
  code=$(curl -sS -m 15 -o "$tmp" -w '%{http_code}' "$HEALTH_URL" 2>/dev/null || true)
  [ -n "$code" ] || code=000
  body=""; [ ! -f "$tmp" ] || body=$(tr -d '\n' <"$tmp")
  flat=$(printf '%s' "$body" | tr -d ' ')

  if [ "$code" = 000 ] || [ -z "$flat" ]; then
    raise api "healthz did not answer ($HEALTH_URL)"
    q=""
  else
    if [ "$code" = 200 ]; then clear api; else
      case "$flat" in *'"checks"'*) ;; *) raise api "healthz answered $code" ;; esac
    fi
    dbok=$(check_ok "$flat" db)
    if [ "$dbok" = false ]; then raise api "database check failing: $(check_detail "$flat" db)"; fi
    ocrok=$(check_ok "$flat" ocr)
    if [ "$ocrok" = false ]; then raise ocr "OCR not ready: $(check_detail "$flat" ocr)"; else clear ocr; fi
    bok=$(check_ok "$flat" backup)
    if [ -z "$BACKUP_DIR" ]; then
      if [ "$bok" = false ]; then raise backup "backup or off-site copy failing: $(check_detail "$flat" backup)"; else clear backup; fi
    fi
    q=$(check_detail "$flat" jobs | sed -n 's/^\([0-9][0-9]*\)pending.*/\1/p')
  fi

  # OCR, read from the OCR service itself (the hosted /healthz shows no details)
  if [ -n "$OCR_URL" ]; then
    oj=$(curl -sS -m 10 "$OCR_URL" 2>/dev/null | tr -d ' \n')
    case "$oj" in *'"ready":true'*) clear ocr ;; *) raise ocr "OCR not ready or not answering ($OCR_URL)" ;; esac
  fi

  # Backup set and off-site copy, read from the status files in the backups volume
  if [ -n "$BACKUP_DIR" ]; then
    bp="$(status_file_problem "$BACKUP_DIR/last_backup.json" "backup" at_unix)
$(status_file_problem "$BACKUP_DIR/offsite_status.json" "off-site copy" last_ok_unix)"
    bp=$(printf '%s' "$bp" | sed '/^$/d' | tr '\n' ';')
    if [ -n "$bp" ]; then raise backup "$bp"; else clear backup; fi
  fi

  # Disk
  used=$(df -P "$DISK_PATH" 2>/dev/null | awk 'NR==2 {gsub("%","",$5); print $5}')
  if [ -n "$used" ] && [ "$used" -ge "$DISK_PCT" ]; then
    raise disk "disk $DISK_PATH is ${used}% full (limit ${DISK_PCT}%)"
  else
    clear disk
  fi

  # Queue backlog: needs several checks in a row, a burst of uploads is normal.
  streak="$STATE/queue.streak"
  if [ -n "$q" ] && [ "$q" -gt "$Q_PENDING" ]; then
    n=$(( $(cat "$streak" 2>/dev/null || echo 0) + 1 ))
    echo "$n" >"$streak"
    if [ "$n" -ge "$Q_CHECKS" ]; then raise queue "$q receipt jobs waiting for $n checks in a row (limit $Q_PENDING)"; fi
  else
    rm -f "$streak"
    clear queue
  fi

  touch "$STATE/heartbeat"
  log "check done: http=$code disk=${used:-?}% queue=${q:-?} failed=$FAILED"
  if [ -n "$PING" ]; then
    if [ "$FAILED" = 0 ]; then curl -fsS -m 10 -o /dev/null "$PING" || log "ping failed"
    else
      case "$PING" in */fail*) u="$PING" ;; *) u="${PING%/}/fail" ;; esac
      curl -fsS -m 10 -o /dev/null "$u" || log "fail-ping failed"
    fi
  fi
  return "$FAILED"
}

case "${1:-loop}" in
  once) run_once; exit $? ;;
  loop)
    [ -n "$WEBHOOK$MAIL_TO" ] || log "no ALERT_WEBHOOK_URL or ALERT_EMAIL_TO set: alerts only go to this log"
    while true; do run_once || true; sleep "$INTERVAL"; done ;;
  *) echo "usage: monitor.sh [loop|once]" >&2; exit 2 ;;
esac
