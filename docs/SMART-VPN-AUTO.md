# Smart VPN Auto (experimental branch)

This branch is a development preview, not a functional standalone VPN release.

- Geo Routing can perform a bounded, interface-bound HTTPS probe through an existing tunnel.
- The independent connection panel detects SSH, WireGuard, AmneziaWG, OpenVPN and related executables without installing or starting them.
- SSH host/user/port and local VPN profile formats can be checked. Files are processed in the browser; no credentials are uploaded or persisted. The checks are preliminary, not an exhaustive security audit or proof of connectivity.
- The failover decision core is isolated and tested independently. It prefers the primary VPN, then a healthy backup VPN, then a confirmed DPI path only when explicitly permitted. Two failures and three primary successes limit flapping. It does not yet control live routes.

Remaining prerequisites: verified platform capabilities (/dev/net/tun, kernel support, route and mark ownership), secure credential transport and storage, SSH host-key verification, independent tunnel lifecycle, strict server-side configuration parsing without executable directives, per-service IPv4/IPv6/DNS routing, persistent supervisor, transactional rollback, and hardware tests. SSH login alone does not guarantee remote IP forwarding or UDP support. Do not assume all VPN providers accept the same credential format.

No automatic release, new default route, firewall change, AWG Manager replacement, or Codex channel change is included. Keep the existing working release installed until the integration is complete.
