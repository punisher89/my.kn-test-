# Independent VPS WireGuard tunnel (experimental)

Open «Туннель Telegram» in Pilot to save a client profile, start or stop the tunnel, and view the last WireGuard handshake. It does not use AWG Manager or modify the default route, DNS or existing Keenetic policies. IPv4 only; IPv6 and Telegram call/media endpoints outside the published Telegram CIDR list are not covered. Actual Keenetic forwarding, hardware offload and policy priority must be verified on the target. Do not use it as a privacy kill switch. It is not started automatically.

## VPS

Set up a dedicated WireGuard peer on your own VPS (Linux with WireGuard, UDP port open, IP forwarding, scoped NAT to WAN). Use the distribution's WireGuard tooling, choose a non-conflicting tunnel subnet and generate a separate keypair on both ends. The server peer AllowedIPs must be the router's tunnel IP /32, and the VPS must route/NAT that IP to the internet. Do not reuse credentials from another VPN. Test the server before changing the router.

## Router

Requires wg, wg-quick (and its bash/ip dependencies), a supported kernel WireGuard implementation, and iptables. Availability varies with KeeneticOS/Entware. The helper does not install packages or touch existing tunnels. Save the following template via the local HTTPS panel (port 9443). The CLI alternative is to create /opt/etc/keenetic-pilot/pilotwg0.conf with mode 600:

```ini
[Interface]
PrivateKey = ROUTER_PRIVATE_KEY
Address = 10.77.66.2/32
MTU = 1380
[Peer]
PublicKey = VPS_PUBLIC_KEY
Endpoint = VPS_IP_OR_HOST:51820
AllowedIPs = 91.108.56.0/22, 91.108.4.0/22, 91.108.8.0/22, 91.108.16.0/22, 91.108.12.0/22, 149.154.160.0/20, 91.105.192.0/23, 91.108.20.0/22, 185.76.151.0/24
PersistentKeepalive = 25
```

Use the private IPv4 /32 assigned to the router by your WireGuard server, and substitute your server port. Keys stay on the device. No hooks, DNS, default route, extra peers or unapproved AllowedIPs are accepted.

For SSH operation from the installed Pilot application directory:

```sh
sh scripts/vps-telegram-wg.sh validate /opt/etc/keenetic-pilot/pilotwg0.conf
sh scripts/vps-telegram-wg.sh start
sh scripts/vps-telegram-wg.sh status
# Rollback:
sh scripts/vps-telegram-wg.sh stop
```

Test with Telegram's in-app proxy disabled on an actual LAN device. Check message sending, downloads and calls independently; the script only validates local IPv4 route lookup, not LAN flow or server egress. Verify other apps stay on the previous path. If there is a problem, stop the helper. IPv6 traffic is not redirected. Do not use on a remote-only router without a fallback management path. If interrupted by power loss, inspect the active marker/interface/chain before reusing it. The helper owns only pilotwg0 and PILOT_WG_TG.
