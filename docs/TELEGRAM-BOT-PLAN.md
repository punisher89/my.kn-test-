# Pilot Telegram management bot — VPS deployment plan

Status: design only. Implement after the beta.6 release; no changes to beta.5, Codex or the beta.6 release branch.

## User experience
- Group/channel: release announcements, version and changelog, successful/failed checks, opt-in critical outages, deduplicated notifications. Never announce a CI build as a published release.
- Private bot chat: Russian-language inline menus, router status and version, uptime and resource usage, service checks, background search progress, queue and selected strategy, VPN/Telegram tunnel status, sanitized diagnostic summary.
- Private actions: check a selected service, enqueue a domain/service search, pause/resume background checks, start/stop/restart Pilot where supported, check available updates, install only explicitly confirmed verified ChatGPT release with rollback. Reuse Pilot's manual priority and normal safety checks. Show actual response, not optimistic success. Never expose or execute arbitrary shell or arbitrary URLs.
- Confirm disruptive operations, display actual target router and action before execution, show running/finished/failed states; no public group control.
- VPS daemon with systemd, no inbound public HTTP service required (Telegram long polling), one small process, bounded network timeouts, backoff, deduplication, restart recovery, per-user rate limiting and audit log without secrets.

## Connection and authorization
- Use private WireGuard/AmneziaWG management network or authenticated router-initiated SSH forwarding, never open Pilot's LAN HTTP port to the Internet.
- Explicit Telegram numeric user-ID allowlist for private controls, separate group-ID for notifications. Recheck every callback and bind confirmations to user/router/action and expiration. Bot token and Pilot credential in restricted VPS secret files, not Git, chat, command line, diagnostics or logs.
- Use a narrowly scoped router management credential; do not share the full administrator token with group or bot users. Implement scoped API first where existing Pilot endpoints lack separation. Read-only default. No credentials echoed, no public admin access, no arbitrary command execution.
- Single router initially. Verify its identity and connectivity; no automatic configuration changes without authorization.

## Release gates
- Finish beta.6 independently and verify its release before bot development. Rebase bot branch onto verified beta.6.
- Unit and integration tests with mock Telegram and Pilot endpoints; verify unauthorized users/group commands/callbacks rejected, duplicate update handling, restart behavior, timeouts and confirmations; test VPS installation and real router explicitly before claiming production readiness.
- Actual VPS and group details will be configured by owner at installation, with no token shared in chat. Deployment is not yet performed.
