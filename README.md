# home-cnc

A minimal command-and-control system for **my own** home devices (Linux + macOS).
Two commands only: **halt** the OS, or **lock** the screen. Nothing hidden, no
remote code execution — the command surface is deliberately tiny.

## How it works

```
  phone/browser (PWA)            VPS: cnc.m53.org                 your devices
 ┌───────────────────┐        ┌────────────────────┐        ┌───────────────────┐
 │  web UI (session) │──POST──▶│  Go server + Caddy  │◀─poll──│  cnc-agent (Go)   │
 │  Lock / Halt      │        │  SQLite + ed25519   │──cmd──▶│  verify sig + run │
 └───────────────────┘        └────────────────────┘   ack  └───────────────────┘
```

- **Transport:** agents make *outbound* HTTPS long-poll requests (`GET /api/v1/poll`,
  held up to 30s). Works behind any NAT/firewall; the server never initiates a
  connection. A newly issued command wakes a waiting poll in ~1s.
- **Authenticity:** every command is ed25519-signed by the server over
  `id|device_id|type|expires_at`. The agent verifies against a public key baked
  into its config before doing anything. Commands expire after 60s, so a queued
  `halt` can't fire when a laptop reconnects tomorrow.
- **Device auth:** each device carries a unique bearer token (shown once at
  creation; only its hash is stored). Revoke by deleting the device.
- **Trust model (v1):** server-trusted — the server holds the signing key.
  Because agents already verify signatures, moving the key into the browser
  later ([#2](https://github.com/mikepea/home-cnc/issues/2)) is a config change,
  not a redesign.

## Repository layout

| Path | What |
|------|------|
| `cmd/server` | control-plane binary (agent API + web UI + `/healthz`) |
| `cmd/agent`  | device daemon |
| `internal/api` | shared command/ack types + canonical signing bytes |
| `internal/store` | `Store` interface + SQLite impl (Postgres-ready — [#3](https://github.com/mikepea/home-cnc/issues/3)) |
| `internal/sign` | server ed25519 key management |
| `internal/server` | HTTP handlers, auth, long-poll notifier, embedded PWA |
| `internal/agent` | poll loop, signature verification, per-OS execution |
| `deploy/` | systemd units + macOS LaunchDaemon plist |
| `Caddyfile` | reverse proxy + automatic TLS for `cnc.m53.org` |

## Quick start (local)

Requires Go 1.26+ (`.tool-versions` pins it for asdf).

```bash
make run          # serves http://127.0.0.1:8080 with admin/changeme
```

On first start with no users, `CNC_ADMIN_USER` / `CNC_ADMIN_PASSWORD` create the
login. The server prints its **signing public key** on startup — you'll paste
that into agent configs.

## Deploying the server (VPS)

1. Point `cnc.m53.org` DNS at the host.
2. Build and install: `make dist` → copy `dist/cnc-server-linux-amd64` to
   `/usr/local/bin/cnc-server`.
3. Create `/etc/home-cnc/server.env`:
   ```
   CNC_ADMIN_USER=mike
   CNC_ADMIN_PASSWORD=<a-long-random-password>
   ```
4. Install `deploy/systemd/home-cnc-server.service`, then
   `systemctl enable --now home-cnc-server`.
5. Run Caddy with the provided `Caddyfile` (auto-TLS on `cnc.m53.org`).

## Enrolling a device

1. In the web UI: **Add a device** (name + OS). Copy the token / `agent.json`
   block it shows **once**.
2. On the device, write `/etc/home-cnc/agent.json` (chmod 600):
   ```json
   {
     "server_url": "https://cnc.m53.org",
     "token": "<device-token>",
     "public_key": "<server-signing-pubkey>"
   }
   ```
3. Install the agent binary and service:
   - **Linux:** `dist/cnc-agent-linux-*` → `/usr/local/bin/cnc-agent`, install
     `deploy/systemd/home-cnc-agent.service`, `systemctl enable --now home-cnc-agent`.
   - **macOS:** `dist/cnc-agent-darwin-*` → `/usr/local/bin/cnc-agent`, install
     `deploy/launchd/org.m53.cnc.agent.plist` to `/Library/LaunchDaemons/`, then
     `sudo launchctl bootstrap system /Library/LaunchDaemons/org.m53.cnc.agent.plist`.

### macOS: why a single root daemon

Halt (`shutdown -h now`) needs root; screen lock must run in the GUI session.
Rather than run two competing pollers, the one **root LaunchDaemon** locks by
dispatching into the console user's session:
`launchctl asuser <consoleUID> …/CGSession -suspend`. Linux is simpler — one
root systemd service does both (`systemctl poweroff`, `loginctl lock-sessions`).

## Command execution

| | Linux | macOS |
|-|-------|-------|
| **halt** | `systemctl poweroff` | `shutdown -h now` |
| **lock** | `loginctl lock-sessions` | `launchctl asuser <uid> CGSession -suspend` |

For `halt` the agent acks *before* executing, so a machine that powers off
mid-cycle won't re-halt on next boot (belt-and-braces with the 60s expiry).

## Roadmap

Tracked as GitHub issues: passkey/WebAuthn ([#1](https://github.com/mikepea/home-cnc/issues/1)),
browser-held E2E signing ([#2](https://github.com/mikepea/home-cnc/issues/2)),
PostgreSQL ([#3](https://github.com/mikepea/home-cnc/issues/3)),
talos-homelab k8s deploy ([#4](https://github.com/mikepea/home-cnc/issues/4)),
Tailscale transport ([#5](https://github.com/mikepea/home-cnc/issues/5)).
