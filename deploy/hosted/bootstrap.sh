#!/usr/bin/env bash
# Prepare a FRESH Ubuntu or Debian server to run the hosted Shop stack. Run once as root
# (sudo), again any time: every step checks before it changes anything.
#
#   sudo SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" ./bootstrap.sh
#   sudo DRY_RUN=1 SSH_PUBKEY=... ./bootstrap.sh        # print what would happen, change nothing
#
# What it does (steps, in order; skip one with SKIP="ufw swap"):
#   packages   base tools
#   user       non-root deploy user (DEPLOY_USER, default "shop"), your key, passwordless sudo off
#   ssh        key-only SSH, no root login (written to a drop-in file; only if the deploy user has a key)
#   ufw        firewall: deny incoming except 22 (SSH_PORT), 80, 443
#   upgrades   unattended security upgrades (no automatic reboot)
#   swap       swap file (SWAP_SIZE, default 2G) with low swappiness
#   docker     Docker Engine + compose plugin from Docker's official apt repository
#   fail2ban   optional (ENABLE_FAIL2BAN=1, default on): bans repeated SSH failures
#   dirs       /srv/shop owned by the deploy user, Docker log rotation defaults
#
# Settings (environment): DEPLOY_USER SSH_PUBKEY (key text) or SSH_PUBKEY_FILE, SSH_PORT (22),
# SWAP_SIZE (2G), ENABLE_FAIL2BAN (1), HARDEN_SSH (1), SKIP, DRY_RUN.
#
# SAFETY: keep your current SSH session open and test a new login as the deploy user before you
# close it. The script refuses to disable root/password login unless the deploy user has a key.
set -euo pipefail

DEPLOY_USER="${DEPLOY_USER:-shop}"
SSH_PORT="${SSH_PORT:-22}"
SWAP_SIZE="${SWAP_SIZE:-2G}"
ENABLE_FAIL2BAN="${ENABLE_FAIL2BAN:-1}"
HARDEN_SSH="${HARDEN_SSH:-1}"
SKIP="${SKIP:-}"
DRY_RUN="${DRY_RUN:-0}"
SSH_PUBKEY="${SSH_PUBKEY:-}"
[ -z "${SSH_PUBKEY_FILE:-}" ] || SSH_PUBKEY="$(cat "$SSH_PUBKEY_FILE")"
export DEBIAN_FRONTEND=noninteractive

say()  { printf '\n== %s\n' "$*"; }
note() { printf '   %s\n' "$*"; }
die()  { echo "bootstrap: $*" >&2; exit 1; }
run()  { if [ "$DRY_RUN" = 1 ]; then printf '   [dry-run] %s\n' "$*"; else "$@"; fi; }
# put FILE: write stdin to FILE only if the content changed
put() {
  local f="$1" tmp; tmp=$(mktemp); cat >"$tmp"
  if [ -f "$f" ] && cmp -s "$tmp" "$f"; then rm -f "$tmp"; note "unchanged $f"; return 0; fi
  if [ "$DRY_RUN" = 1 ]; then printf '   [dry-run] would write %s\n' "$f"; rm -f "$tmp"; return 0; fi
  install -m "${PUT_MODE:-644}" "$tmp" "$f"; rm -f "$tmp"; note "wrote $f"
}
skipped() { case " $SKIP " in *" $1 "*) note "skipped $1"; return 0 ;; esac; return 1; }

[ "$(id -u)" = 0 ] || [ "$DRY_RUN" = 1 ] || die "run as root: sudo ./bootstrap.sh"
[ -r /etc/os-release ] || die "no /etc/os-release"
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-}" in ubuntu|debian) ;; *) die "this script supports Ubuntu and Debian, found '${ID:-unknown}'" ;; esac
CODENAME="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}"
[ -n "$CODENAME" ] || die "cannot tell the release codename"
[[ "$DEPLOY_USER" =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] || die "bad DEPLOY_USER '$DEPLOY_USER'"
[[ "$SSH_PORT" =~ ^[0-9]+$ ]] || die "bad SSH_PORT"
echo "bootstrap: $PRETTY_NAME, deploy user '$DEPLOY_USER', ssh port $SSH_PORT, dry-run=$DRY_RUN"

step_packages() {
  say "packages"
  run apt-get update -y
  run apt-get install -y --no-install-recommends ca-certificates curl gnupg sudo openssh-server lsb-release unattended-upgrades apt-listchanges
}

step_user() {
  say "deploy user"
  if id "$DEPLOY_USER" >/dev/null 2>&1; then note "user $DEPLOY_USER exists"; else run adduser --disabled-password --gecos "Shop deploy" "$DEPLOY_USER"; fi
  home=$(getent passwd "$DEPLOY_USER" | cut -d: -f6 || true); home="${home:-/home/$DEPLOY_USER}"
  if [ -n "$SSH_PUBKEY" ]; then
    case "$SSH_PUBKEY" in ssh-ed25519\ *|ssh-rsa\ *|ecdsa-sha2-*|sk-*) ;; *) die "SSH_PUBKEY does not look like a public key" ;; esac
    run install -d -m 700 -o "$DEPLOY_USER" -g "$DEPLOY_USER" "$home/.ssh"
    if [ -f "$home/.ssh/authorized_keys" ] && grep -qxF "$SSH_PUBKEY" "$home/.ssh/authorized_keys"; then note "key already authorised"
    elif [ "$DRY_RUN" = 1 ]; then note "[dry-run] would add the key"
    else printf '%s\n' "$SSH_PUBKEY" >>"$home/.ssh/authorized_keys"; chmod 600 "$home/.ssh/authorized_keys"; chown "$DEPLOY_USER:$DEPLOY_USER" "$home/.ssh/authorized_keys"; note "key added"; fi
  else
    note "no SSH_PUBKEY given: SSH hardening will be skipped unless the user already has a key"
  fi
  # Sudo with a password is not possible for a passwordless account; give sudo without password to
  # this one account only so deploys and updates work. Remove /etc/sudoers.d/shop-deploy to tighten.
  printf '%s ALL=(ALL) NOPASSWD:ALL\n' "$DEPLOY_USER" | PUT_MODE=440 put /etc/sudoers.d/shop-deploy
}

step_ssh() {
  say "ssh hardening"
  [ "$HARDEN_SSH" = 1 ] || { note "HARDEN_SSH=0, leaving sshd alone"; return 0; }
  local home ak; home=$(getent passwd "$DEPLOY_USER" | cut -d: -f6 || true); ak="${home:-/home/$DEPLOY_USER}/.ssh/authorized_keys"
  if [ "$DRY_RUN" != 1 ] && { [ ! -s "$ak" ] || ! grep -Eq '^(ssh-|ecdsa-|sk-)' "$ak"; }; then
    note "REFUSING to disable password/root login: $ak has no key (would lock you out). Pass SSH_PUBKEY and re-run."
    return 0
  fi
  if ! grep -Eqi '^\s*Include\s+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config 2>/dev/null; then
    note "sshd_config has no Include for sshd_config.d; adding it at the top"
    [ "$DRY_RUN" = 1 ] || { cp -n /etc/ssh/sshd_config /etc/ssh/sshd_config.bak-shop; sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config; }
  fi
  run install -d -m 755 /etc/ssh/sshd_config.d
  put /etc/ssh/sshd_config.d/10-shop-hardening.conf <<EOF2
# Managed by deploy/hosted/bootstrap.sh
Port $SSH_PORT
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
AllowUsers $DEPLOY_USER
MaxAuthTries 3
LoginGraceTime 30
X11Forwarding no
EOF2
  if [ "$DRY_RUN" = 1 ]; then note "[dry-run] sshd -t and reload"; return 0; fi
  mkdir -p /run/sshd
  if sshd -t; then
    if command -v systemctl >/dev/null && systemctl is-active --quiet ssh 2>/dev/null; then systemctl reload ssh
    elif command -v systemctl >/dev/null && systemctl is-active --quiet sshd 2>/dev/null; then systemctl reload sshd
    else note "sshd not running under systemd here; the new config applies at next start"; fi
  else
    rm -f /etc/ssh/sshd_config.d/10-shop-hardening.conf
    die "sshd -t rejected the config; the drop-in was removed"
  fi
}

step_ufw() {
  say "firewall (ufw)"
  run apt-get install -y --no-install-recommends ufw
  run ufw default deny incoming
  run ufw default allow outgoing
  run ufw allow "${SSH_PORT}/tcp" comment ssh
  run ufw allow 80/tcp comment http-acme
  run ufw allow 443 comment https   # tcp and udp (HTTP/3)
  run ufw --force enable
  note "Docker publishes ports through its own iptables rules, which bypass ufw. That is safe here"
  note "because the stack publishes only Caddy's 80 and 443. Never add 'ports:' to another service."
}

step_upgrades() {
  say "unattended security upgrades"
  run apt-get install -y --no-install-recommends unattended-upgrades
  put /etc/apt/apt.conf.d/20auto-upgrades <<'EOF2'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF2
  put /etc/apt/apt.conf.d/52shop-unattended <<'EOF2'
// Managed by deploy/hosted/bootstrap.sh. Security origins stay as the distribution default.
// No automatic reboot: you reboot in a quiet moment (kernel updates show /var/run/reboot-required).
Unattended-Upgrade::Automatic-Reboot "false";
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
EOF2
}

step_swap() {
  say "swap"
  if swapon --show=NAME --noheadings 2>/dev/null | grep -q .; then note "swap already active"; else
    if [ ! -f /swapfile ]; then
      run fallocate -l "$SWAP_SIZE" /swapfile || run dd if=/dev/zero of=/swapfile bs=1M count=$(( ${SWAP_SIZE%G} * 1024 ))
      run chmod 600 /swapfile
      run mkswap /swapfile
    fi
    run swapon /swapfile || note "could not enable swap here (container?)"
  fi
  if ! grep -q '^/swapfile ' /etc/fstab 2>/dev/null; then
    if [ "$DRY_RUN" = 1 ]; then note "[dry-run] would add /swapfile to /etc/fstab"; else echo '/swapfile none swap sw 0 0' >>/etc/fstab; note "added to fstab"; fi
  fi
  put /etc/sysctl.d/60-shop-swap.conf <<'EOF2'
vm.swappiness=10
EOF2
  [ "$DRY_RUN" = 1 ] || sysctl -q -p /etc/sysctl.d/60-shop-swap.conf >/dev/null 2>&1 || note "sysctl not applied here (container?)"
}

step_docker() {
  say "Docker Engine from Docker's official apt repository"
  run install -m 0755 -d /etc/apt/keyrings
  if [ ! -s /etc/apt/keyrings/docker.asc ]; then
    run curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
    run chmod a+r /etc/apt/keyrings/docker.asc
  else note "docker key present"; fi
  arch=$(dpkg --print-architecture)
  put /etc/apt/sources.list.d/docker.list <<EOF2
deb [arch=$arch signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID $CODENAME stable
EOF2
  run apt-get update -y
  run apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  put /etc/docker/daemon.json <<'EOF2'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true
}
EOF2
  run usermod -aG docker "$DEPLOY_USER"
  note "$DEPLOY_USER is in the docker group: that is root-equivalent, so protect that account's SSH key."
  if command -v systemctl >/dev/null && [ "$DRY_RUN" != 1 ]; then systemctl enable --now docker 2>/dev/null || note "docker service not started here (container?)"; fi
}

step_fail2ban() {
  say "fail2ban (optional)"
  [ "$ENABLE_FAIL2BAN" = 1 ] || { note "ENABLE_FAIL2BAN=0, skipped"; return 0; }
  run apt-get install -y --no-install-recommends fail2ban
  put /etc/fail2ban/jail.d/shop.local <<EOF2
[sshd]
enabled = true
port = $SSH_PORT
maxretry = 4
findtime = 10m
bantime = 1h
EOF2
  if command -v systemctl >/dev/null && [ "$DRY_RUN" != 1 ]; then systemctl enable --now fail2ban 2>/dev/null && systemctl reload fail2ban 2>/dev/null || note "fail2ban not started here (container?)"; fi
}

step_dirs() {
  say "directories"
  run install -d -m 755 -o "$DEPLOY_USER" -g "$DEPLOY_USER" /srv/shop
  note "put the repository (or just deploy/hosted/) in /srv/shop; see RUNBOOK.md"
}

for s in packages user ssh ufw upgrades swap docker fail2ban dirs; do
  skipped "$s" || "step_$s"
done

say "done"
note "Next: open a SECOND terminal and check 'ssh -p $SSH_PORT $DEPLOY_USER@<server>' works before closing this one."
note "Then follow deploy/hosted/RUNBOOK.md, 'First deploy'."
