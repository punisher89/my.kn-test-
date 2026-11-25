#!/bin/sh
# Explicitly started loopback-only SOCKS proxy; no firewall or routing changes.
set +x
set -eu
umask 077
die() { printf '%s\n' "$*" >&2; exit 1; }
[ "$#" -eq 4 ] || die "Usage: $0 HOST PORT USER password|key"
host=$1 port=$2 login=$3 auth=$4
case "$host" in ''|-*|*[!a-zA-Z0-9.:-]*) die "Invalid VPS host";; esac
case "$login" in ''|-*|*[!a-zA-Z0-9_.-]*) die "Invalid login";; esac
case "$port" in ''|*[!0-9]*) die "Invalid port";; esac
[ "${#port}" -le 5 ] && [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || die "Invalid port"
case "$auth" in password|key) ;; *) die "Invalid authentication mode";; esac
command -v ssh >/dev/null 2>&1 || die "OpenSSH client required"
base=/opt/etc/keenetic-pilot
known=$base/vps-known-hosts
key=$base/vps-identity
[ -s "$known" ] || die "Missing verified server host key: $known"
lock=$base/vps-socks.lock
mkdir "$lock" 2>/dev/null || die "Proxy already running or stale lock (inspect before removing)"
child= delay=5 password= tty_state=
cleanup() {
    if [ -n "$tty_state" ]; then stty "$tty_state" </dev/tty 2>/dev/null || :; fi
    if [ -n "$child" ]; then kill "$child" 2>/dev/null || :; wait "$child" 2>/dev/null || :; fi
    unset password
    rmdir "$lock" 2>/dev/null || :
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
if [ "$auth" = password ]; then
    command -v sshpass >/dev/null 2>&1 || die "Password mode requires sshpass"
    tty_state=$(stty -g </dev/tty) || die "Interactive SSH terminal required"
    printf 'VPS password (not stored): ' >/dev/tty
    stty -echo </dev/tty
    IFS= read -r password </dev/tty || die "Password input cancelled"
    stty "$tty_state" </dev/tty
    tty_state=
    printf '\n' >/dev/tty
    [ -n "$password" ] || die "Empty password"
else
    [ -s "$key" ] || die "Missing dedicated SSH identity: $key"
fi
set -- -F /dev/null -N -n -T -D 127.0.0.1:10880 -p "$port" -l "$login" \
    -o "UserKnownHostsFile=$known" -o GlobalKnownHostsFile=/dev/null \
    -o StrictHostKeyChecking=yes -o ExitOnForwardFailure=yes \
    -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
    -o NumberOfPasswordPrompts=1 -o ForwardAgent=no -o ForwardX11=no
if [ "$auth" = password ]; then
    set -- "$@" -o PreferredAuthentications=password -o PubkeyAuthentication=no -o BatchMode=no
else
    set -- "$@" -o BatchMode=yes -o IdentitiesOnly=yes -i "$key" -o PreferredAuthentications=publickey
fi
printf '%s\n' "SOCKS endpoint: 127.0.0.1:10880 (router only; no routes changed)"
while :; do
    started=$(date +%s)
    if [ "$auth" = password ]; then
        # Password travels through a pipe, not argv, environment or a file.
        # sshpass reads fd 3; SSH has its own stdin redirected by -n.
        printf '%s\n' "$password" | sshpass -d 3 ssh "$@" -- "$host" 3<&0 &
    else
        ssh "$@" -- "$host" &
    fi
    child=$!
    rc=0
    wait "$child" || rc=$?
    child=
    case "$rc" in 5|6|7) die "SSH authentication or host key rejected; no retry";; esac
    now=$(date +%s)
    if [ "$((now-started))" -ge 120 ]; then delay=5; fi
    printf 'SSH disconnected (exit %s); retry in %s seconds\n' "$rc" "$delay" >&2
    sleep "$delay" &
    child=$!
    wait "$child" || :
    child=
    delay=$((delay*2))
    [ "$delay" -le 60 ] || delay=60
done
