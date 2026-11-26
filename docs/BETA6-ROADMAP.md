# Beta.6 — development

Base: published 0.2.0-beta.5. Keep the working beta.5 and Codex update channel unchanged; no automatic publication from this branch.

- [ ] VPN subscriptions: explicit HTTPS URL preview and separate local file import (TXT/base64 URI lists initially); show loading, success, error, protocol counts and unsupported formats in dismissible preview window. Keep secrets out of logs and never transmit private data over unsecured HTTP.
- [ ] Persist a successful local import separately from URL-backed subscriptions; disable refresh for local files. No implicit tunnel activation or routing changes.
- [ ] VPS over SSH: password/key authentication, host-key verification, secure credential entry, connection test, start/stop and clear status. Verify dependencies and do not expose a proxy publicly.
- [ ] VPN server selection and connection checks; actual supported engines and capability checks, explicit user activation; no automatic route replacement.
- [ ] Improve diagnostics for DNS/route conflicts and subscription fetch errors; keep UI state across polling.
- [ ] Verify beta.5 history retention, learning restoration, adaptive selection, backup, notification history and rollback against real failure cases.
- [ ] Measure resource usage on ARM64 and MIPSEL and test UI on Windows browser. Keep existing nfqws2 engine.
- [ ] Run full CI, verify update/rollback and only then publish independently to ChatGPT channel.
