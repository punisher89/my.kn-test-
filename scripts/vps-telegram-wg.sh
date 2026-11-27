#!/bin/sh
# Experimental independent WireGuard tunnel. IPv4 Telegram prefixes only.
# No default route, no DNS changes, no Keenetic/AWG Manager changes.
set -eu
umask 077
base=${PILOT_WG_CONFIG_DIR:-/opt/etc/keenetic-pilot}
iface=pilotwg0
config=$base/$iface.conf
state=$base/$iface.active
lock=$base/$iface.lock
chain=PILOT_WG_TG
allowed='91.108.56.0/22,91.108.4.0/22,91.108.8.0/22,91.108.16.0/22,91.108.12.0/22,149.154.160.0/20,91.105.192.0/23,91.108.20.0/22,185.76.151.0/24'
die(){ printf '%s\n' "$*" >&2; exit 1; }
validate(){
 [ -f "$1" ] && [ ! -L "$1" ] || die 'Missing regular WireGuard config'
 # Exact allowlist, single peer, no shell hooks, DNS, Table or default route.
 awk -v allowed="$allowed" '
 function fail(){bad=1; exit 1}
 function address(v, a,n) {
  if(v !~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+\/32$/)return 0
  sub(/\/32$/,"",v); n=split(v,a,".")
  if(n!=4||a[1]>255||a[2]>255||a[3]>255||a[4]>255)return 0
  return (a[1]==10)||(a[1]==172&&a[2]>=16&&a[2]<=31)||(a[1]==192&&a[2]==168)
 }
 function key(v){return length(v)==44 && v ~ /^[A-Za-z0-9+\/]+=$/}
 /^[[:space:]]*($|#)/ {next}
 {
   sub(/\r$/,"")
   gsub(/^[[:space:]]+|[[:space:]]+$/,"")
   if($0=="[Interface]"){if(section!="")fail();section="i";next}
   if($0=="[Peer]"){if(section!="i")fail();section="p";next}
   pos=index($0,"=");if(!pos)fail()
   k=substr($0,1,pos-1); v=substr($0,pos+1)
   gsub(/^[[:space:]]+|[[:space:]]+$/,"",k);gsub(/^[[:space:]]+|[[:space:]]+$/,"",v)
   if(seen[section,k]++)fail()
   if(section=="i" && k=="PrivateKey"){if(!key(v))fail();next}
   if(section=="i" && k=="Address"){if(!address(v))fail();next}
   if(section=="i" && k=="MTU"){if(v !~ /^[0-9]+$/||v<1280||v>1500)fail();next}
   if(section=="p" && (k=="PublicKey"||k=="PresharedKey")){if(!key(v))fail();next}
   if(section=="p" && k=="Endpoint"){if(v !~ /^[A-Za-z0-9.-]+:[0-9]+$/||v ~ /^-/)fail(); port=substr(v,index(v,":")+1)+0; if(port<1||port>65535)fail();next}
   if(section=="p" && k=="PersistentKeepalive"){if(v!="25")fail();next}
   if(section=="p" && k=="AllowedIPs"){gsub(/[[:space:]]/,"",v);if(v!=allowed)fail();next}
   fail()
 }
 END{
  if(bad||!seen["i","PrivateKey"]||!seen["i","Address"]||!seen["p","PublicKey"]||!seen["p","Endpoint"]||!seen["p","AllowedIPs"]||!seen["p","PersistentKeepalive"])exit 1
 }' "$1" || die 'Rejected config: use the documented single-peer IPv4 template'
}
case "${1:-}" in
 validate) [ "$#" -eq 2 ] || die 'validate FILE'; validate "$2"; echo 'Config syntax and Telegram allowlist OK'; exit 0;;
 status)
  if [ ! -f "$state" ]; then echo 'Not managed / stopped'; exit 0; fi
  ip link show dev "$iface" || exit 1
  wg show "$iface" latest-handshakes
  echo 'Handshake alone does not prove Telegram works; verify real client traffic.'
  exit 0;;
 start|stop) ;;
 *) die 'Usage: vps-telegram-wg.sh validate FILE | start | status | stop';;
esac
[ "$#" -eq 1 ] || die 'Unexpected arguments'
[ "$(id -u)" -eq 0 ] || die 'Root required'
for cmd in wg wg-quick ip iptables; do command -v "$cmd" >/dev/null 2>&1 || die "Missing $cmd"; done
[ -d "$base" ] || die 'Pilot config directory missing'
mkdir "$lock" 2>/dev/null || die 'Another operation or stale lock; inspect before removing'
madechain=0 madejump=0 started=0 success=0
cleanup(){
 if [ "$success" -ne 1 ]; then
  if [ "$madejump" -eq 1 ]; then iptables -t nat -D POSTROUTING -o "$iface" -j "$chain" 2>/dev/null || :; fi
  if [ "$madechain" -eq 1 ]; then iptables -t nat -F "$chain" 2>/dev/null || :; iptables -t nat -X "$chain" 2>/dev/null || :; fi
  if [ "$started" -eq 1 ]; then wg-quick down "$config" >/dev/null 2>&1 || :; fi
 fi
 rmdir "$lock" 2>/dev/null || :
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
if [ "$1" = stop ]; then
 [ -f "$state" ] || die 'No Pilot-owned tunnel marker; refusing to remove unknown interface'
 # Do not flush other chains or routing tables.
 if iptables -t nat -C POSTROUTING -o "$iface" -j "$chain" 2>/dev/null; then
  iptables -t nat -D POSTROUTING -o "$iface" -j "$chain"
 fi
 if iptables -t nat -L "$chain" >/dev/null 2>&1; then
  iptables -t nat -F "$chain"; iptables -t nat -X "$chain"
 fi
 if ip link show dev "$iface" >/dev/null 2>&1; then wg-quick down "$config"; fi
 rm -f "$state"
 success=1
 echo 'Pilot Telegram tunnel stopped'
 exit 0
fi
[ ! -e "$state" ] || die 'Already managed: use status or stop'
! ip link show dev "$iface" >/dev/null 2>&1 || die 'Interface already exists; refusing to modify it'
validate "$config"
[ "$(stat -c '%a' "$config")" = 600 ] || die 'Config must have mode 600'
# Require the NAT chain name to be unused; failure leaves existing chain alone.
iptables -t nat -N "$chain"
madechain=1
iptables -t nat -A "$chain" -j MASQUERADE
started=1
wg-quick up "$config"
# wg-quick may skip routes that already exist. Verify route lookup before enabling NAT.
oldifs=$IFS; IFS=,
for prefix in $allowed; do
 target=${prefix%/*}
 ip -4 route get "$target" | grep -Eq "(^| )dev $iface( |$)" || die "Route conflict: $prefix"
done
IFS=$oldifs
iptables -t nat -A POSTROUTING -o "$iface" -j "$chain"
madejump=1
: > "$state"
success=1
echo 'Telegram IPv4 routes configured; test from a LAN client. IPv6 is not managed.'
echo 'PersistentKeepalive is enabled; no default route was installed.'
