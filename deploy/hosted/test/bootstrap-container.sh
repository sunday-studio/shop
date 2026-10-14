#!/usr/bin/env bash
# Runs bootstrap.sh inside a throwaway Debian container (never on the host): once for real, once
# more to prove it is idempotent, plus a dry run for the steps a container cannot do (ufw, swap).
# Plain `docker run --rm`: this does not use docker compose.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="${BOOTSTRAP_TEST_IMAGE:-debian:bookworm-slim}"
echo "bootstrap test: throwaway container from $IMAGE (not this machine)"
docker run --rm -v "$HERE/../bootstrap.sh:/bootstrap.sh:ro" "$IMAGE" bash -euo pipefail -c '
  apt-get update -qq >/dev/null && apt-get install -y -qq --no-install-recommends ca-certificates curl >/dev/null
  KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTestingOnlyFakeKeyForTestingOnly test@example"
  export SSH_PUBKEY="$KEY" SKIP="ufw swap"

  echo "--- dry run of every step (no changes)"
  DRY_RUN=1 SKIP="" /bootstrap.sh >/tmp/dry.txt 2>&1 || { cat /tmp/dry.txt; exit 1; }
  ! id shop >/dev/null 2>&1 || { echo "dry run created a user"; exit 1; }
  allows=$(grep -E "ufw allow" /tmp/dry.txt | sed -E "s/.*ufw allow ([0-9]+).*/\1/" | sort -u | tr "\n" " ")
  [ "$allows" = "22 443 80 " ] || { echo "ufw would allow: $allows"; exit 1; }
  grep -q "ufw default deny incoming" /tmp/dry.txt

  echo "--- real run 1"
  /bootstrap.sh >/tmp/run1.txt 2>&1 || { tail -n 40 /tmp/run1.txt; exit 1; }
  echo "--- real run 2 (idempotent)"
  /bootstrap.sh >/tmp/run2.txt 2>&1 || { tail -n 40 /tmp/run2.txt; exit 1; }

  echo "--- checks"
  id shop >/dev/null
  [ "$(grep -c . /home/shop/.ssh/authorized_keys)" = 1 ]
  [ "$(stat -c %a /home/shop/.ssh)" = 700 ]
  grep -q "^PermitRootLogin no" /etc/ssh/sshd_config.d/10-shop-hardening.conf
  grep -q "^PasswordAuthentication no" /etc/ssh/sshd_config.d/10-shop-hardening.conf
  mkdir -p /run/sshd && sshd -t
  visudo -c >/dev/null
  grep -q "Unattended-Upgrade \"1\"" /etc/apt/apt.conf.d/20auto-upgrades
  grep -q "download.docker.com/linux/debian bookworm stable" /etc/apt/sources.list.d/docker.list
  docker --version && dpkg -s docker-compose-plugin | grep -q "Status: install ok"
  id -nG shop | grep -qw docker
  [ -f /etc/fail2ban/jail.d/shop.local ]
  [ -d /srv/shop ] && [ "$(stat -c %U /srv/shop)" = shop ]
  grep -q "unchanged" /tmp/run2.txt
  [ "$(grep -c "   wrote " /tmp/run2.txt)" = 0 ] || { echo "second run changed files:"; grep "   wrote " /tmp/run2.txt; exit 1; }

  echo "--- no key given: ssh hardening must refuse"
  userdel -r shop >/dev/null 2>&1 || true
  rm -f /etc/ssh/sshd_config.d/10-shop-hardening.conf
  SSH_PUBKEY="" DEPLOY_USER=other SKIP="ufw swap docker fail2ban upgrades packages" /bootstrap.sh >/tmp/nokey.txt 2>&1
  grep -q "REFUSING to disable" /tmp/nokey.txt
  [ ! -f /etc/ssh/sshd_config.d/10-shop-hardening.conf ]
  echo BOOTSTRAP-OK
' | tail -n 25
