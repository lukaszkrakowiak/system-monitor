# TODO

Planned work, roughly in priority order. Items 1–3 are what make the monitor fit for long-running production use.

## Top priority

1. **Data retention.** `cli_command_logs`, `login_failures`, `ssh_sessions` and `notifications` grow without bound.
   Add background pruning (default 90 days, configurable in Settings) and optional CSV export.
2. **Host metrics.** Add collectors for disk space and inodes, RAM/swap, load average and TLS certificate expiry
   (read from `/proc`/`statfs`, no extra privileges). Feed them into the existing OK/WARN/CRIT model with configurable
   thresholds so the current alert rules cover them.
3. **Alert rules on audit data.**
   - Failed logins with a threshold (e.g. 5 attempts from one IP within 10 minutes) instead of one alert per attempt.
   - Command patterns (`rm -rf`, `curl | sh`, `useradd`, edits of `/etc/passwd`, `sudo su`, ...).
   - First login of a user from a new IP.

## Monitor resilience

4. **Mute and rate limiting.** Maintenance mode ("mute for 30 min, deploying"), digest mails instead of one per failing
   service, and a per-hour cap on notifications.
5. **Heartbeat (dead man's switch).** Periodic ping to an external URL (e.g. Healthchecks.io) so you are told when the whole
   host, and therefore the monitor, goes down.
6. **Incident history.** Store state changes (when it broke, how long, uptime %) and show a timeline in the UI. The data
   already passes through the alert engine.

## Open source project

7. **GitHub Actions.** Run `go test -race`, `vitest`, build the Linux binary and publish a release with checksums on tags.
   Add `SECURITY.md`, `CONTRIBUTING.md` and Dependabot.
8. **One-step install.** An `install.sh` or `.deb` package that does what `deploy/README.md` describes manually today.
9. **End-to-end test on a real system.** Container or VM with systemd, auditd and sshd where CI exercises the whole path
   (login → audit entry → alert). The PAM and auditd paths are currently covered only by fixtures and manual checks.

## UI security

10. **Proper login for the UI** instead of a token in `sessionStorage`, rate limiting of failed API authentication,
    built-in TLS (or clear reverse-proxy docs), and a settings change log ("who changed which rule, when").

## Smaller improvements

- Per-rule channel selection and severity.
- Per-user login-time rules (e.g. `deploy` may log in at night, others may not).
- Split the frontend bundle (about 970 kB in one chunk) and add a Polish translation of the UI.
- More connectors (Slack, Teams, ...) through the registry in `internal/notify`.
- Multi-server view in one dashboard (larger architectural change, later).

## Known gaps

- The email connector has only been tested against a local fake SMTP server, not a real provider or TLS.
- The UI has not been checked in a real browser against the CSP.
- `ingest-pam` file ownership of the WAL files has not been verified on a real VM.
