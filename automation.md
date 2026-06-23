# Automated Sync (cron / systemd timers)

> How to run `vv sync` on a schedule without manual intervention.

---

## The Problem

`vv sync` shells out to `ssh` (for delegation) and `rclone` (for SFTP bisync). Both need to
authenticate to remote hosts. In an interactive session, this works because:

- **ssh-agent** holds decrypted keys in memory
- **`~/.ssh/config`** provides host aliases, keys, and users
- You might type a passphrase once per session

Cron jobs have none of that: no TTY, no agent, no passphrase prompt.

---

## Option Summary

| Option | Security | Complexity | Best for |
|--------|----------|------------|----------|
| [1. Passwordless key](#1-passwordless-ssh-key) | ⚠️ Moderate | Low | Simple cron, trusted LAN |
| [2. Key + ssh-agent socket](#2-ssh-agent-socket-in-cron) | ✅ Good | Medium | Laptops, user sessions |
| [3. systemd timer](#3-systemd-timer-recommended) | ✅ Good | Medium | Linux servers, recommended |
| [4. Tailscale SSH](#4-tailscale-ssh) | ✅ Best | Low–Medium | Tailscale users |
| [5. gpg-agent as SSH agent](#5-gpg-agent-as-ssh-agent) | ✅ Good | Medium | YubiKey / smartcard users |
| [6. rclone API (future)](#6-rclone-api-future) | ✅ Good | High (v2) | Future direction |

---

## 1. Passwordless SSH Key

Create a dedicated key with no passphrase, used only for vevault syncs.

```bash
# On each remote host:
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_vv -N "" -C "vevault-sync"
ssh-copy-id -i ~/.ssh/id_ed25519_vv.pub central

# On central:
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_vv -N "" -C "vevault-sync"
ssh-copy-id -i ~/.ssh/id_ed25519_vv.pub remote1
ssh-copy-id -i ~/.ssh/id_ed25519_vv.pub remote2
```

**`~/.ssh/config` (all hosts):**
```
Host central
    HostName central.example.com
    User allen
    IdentityFile ~/.ssh/id_ed25519_vv
    IdentitiesOnly yes

Host remote1 remote2
    User allen
    IdentityFile ~/.ssh/id_ed25519_vv
    IdentitiesOnly yes
```

**Crontab (remote host):**
```
*/15 * * * * /usr/local/bin/vv sync >> ~/.local/share/vevault/sync.log 2>&1
```

**Crontab (central):**
```
*/15 * * * * /usr/local/bin/vv sync >> ~/.local/share/vevault/sync.log 2>&1
```

**Pros:**
- Dead simple, works everywhere
- No external services needed

**Cons:**
- ⚠️ Anyone who steals the private key gets SSH access to all vevault hosts
- No passphrase protection on the key itself

**Mitigations:**
- Restrict the key to specific commands on the central host (see [Command Restriction](#command-restriction))
- Store the key on an encrypted filesystem (which vevault v1.1 will provide)
- Rotate the key regularly

---

## 2. SSH Agent Socket in Cron

Use `ssh-agent` and point cron at the agent socket. Works if you stay logged in (or use a
long-running agent).

```bash
# Start agent and add key (once per session):
eval $(ssh-agent -a ~/.ssh/agent.sock)
ssh-add ~/.ssh/id_ed25519_vv

# Crontab:
SSH_AUTH_SOCK=/home/allen/.ssh/agent.sock
*/15 * * * * /usr/local/bin/vv sync >> ~/.local/share/vevault/sync.log 2>&1
```

**Pros:**
- Key stays encrypted (has passphrase)
- Uses existing agent infrastructure

**Cons:**
- Agent dies on reboot — needs manual restart or a user session
- Fragile: if agent dies, cron silently fails
- Not suitable for headless servers

---

## 3. systemd Timer (Recommended for Linux)

Instead of cron, use a user-level systemd timer. It runs in the user session context, so
`ssh-agent` / `SSH_AUTH_SOCK` is automatically available.

```ini
# ~/.config/systemd/user/vv-sync.service
[Unit]
Description=Vevault sync

[Service]
Type=oneshot
ExecStart=/usr/local/bin/vv sync
StandardOutput=append:%h/.local/share/vevault/sync.log
StandardError=append:%h/.local/share/vevault/sync.log
```

```ini
# ~/.config/systemd/user/vv-sync.timer
[Unit]
Description=Vevault sync timer

[Timer]
OnCalendar=*:0/15
Persistent=true     # Run missed jobs after suspend/boot

[Install]
WantedBy=timers.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now vv-sync.timer

# Check status:
systemctl --user status vv-sync.timer
systemctl --user list-timers
```

For keyboard-interactive servers (no desktop session), use `lingering` so the user session
persists without a login:

```bash
sudo loginctl enable-linger allen
```

Then add `Environment=SSH_AUTH_SOCK=...` to the service unit with a key already loaded in a
system-scope agent.

**Pros:**
- Native Linux scheduling
- `Persistent=true` catches up after suspend/boot
- Runs in user session context (agent available)
- Better logging via journald

**Cons:**
- Linux-only (no macOS launchd support yet, but the same pattern applies)
- Still needs agent set up

---

## 4. Tailscale SSH

If all hosts are on Tailscale, use [Tailscale SSH](https://tailscale.com/kb/1193/tailscale-ssh).
It authenticates via Tailscale identity — no SSH keys at all.

```bash
# Enable Tailscale SSH on all hosts:
tailscale up --ssh

# ~/.ssh/config (all hosts):
Host central
    HostName central.tails-scales.ts.net

Host remote1 remote2
    HostName %h.tails-scales.ts.net

# No IdentityFile needed — Tailscale handles auth.
```

Then `vv sync` just works because `ssh central` authenticates via Tailscale's WireGuard
identity. No keys, no passphrases, no agent.

**Pros:**
- No SSH keys to manage
- Tailscale identity is the auth factor (strong)
- Works transparently with cron/systemd
- Solves NAT traversal too

**Cons:**
- Requires Tailscale on all hosts
- Requires Tailscale SSH to be enabled (on by default in newer versions)
- Central must trust Tailscale's identity verification

---

## 5. gpg-agent as SSH Agent

If you use a YubiKey, smartcard, or GPG key for SSH, `gpg-agent` can serve as the SSH agent.
It persists across sessions and can be configured to cache passphrases.

```bash
# ~/.gnupg/gpg-agent.conf
enable-ssh-support
default-cache-ttl 3600
max-cache-ttl 7200

# Shell config (~/.bashrc / ~/.zshrc):
export SSH_AUTH_SOCK=$(gpgconf --list-dirs agent-ssh-socket)

# Crontab:
SSH_AUTH_SOCK=/run/user/1000/gnupg/S.gpg-agent.ssh
*/15 * * * * /usr/local/bin/vv sync >> ~/.local/share/vevault/sync.log 2>&1
```

**Pros:**
- Hardware-backed key (YubiKey) is very secure
- gpg-agent persists independently of login sessions
- Works with cron

**Cons:**
- Complex setup
- YubiKey must be physically present (bad for headless servers)
- GPG configuration can be fragile

---

## 6. rclone API (Future)

A future version of vevault could run an `rclone serve` daemon on the central host, exposing
vaults via an authenticated API (WebDAV, SFTP, or a custom HTTP endpoint). Remotes would
connect directly without SSH:

```
# Central runs:
vv serve          # starts rclone serve webdav on localhost:8080
                  # with TLS + API token auth

# Remote cron:
*/15 * * * * vv sync
# vv sync connects to https://central:8080/ with stored token
```

This would eliminate the SSH dependency entirely for the sync path. SSH would only be
needed for the subscribe/unsubscribe delegation (one-time setup).

**Pros:**
- No SSH keys at all for ongoing sync
- Token-based auth (scoped, revokable)
- Simpler cron setup

**Cons:**
- Requires a daemon on central (adds complexity)
- Security surface: HTTP/WebDAV endpoint to secure
- Not yet implemented

---

## Command Restriction

For the passwordless key approach, you can restrict the SSH key to only run `vv updates`:

```
# In ~/.ssh/authorized_keys on central:
command="/usr/local/bin/vv updates ${SSH_ORIGINAL_COMMAND#* }",no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty ssh-ed25519 AAAAC3... vevault-sync
```

For remote hosts, restrict to only running `vv updates` (since that's all central sends):

```
# In ~/.ssh/authorized_keys on each remote:
command="echo 'This key is for rclone SFTP only'",no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty ssh-ed25519 AAAAC3... vevault-sync
```

Actually, the SFTP path used by rclone goes through the SSH subsystem, not command
execution. So the remote key can't be easily command-restricted for SFTP. But you can
restrict it to SFTP-only:

```
restrict,command="internal-sftp" ssh-ed25519 AAAAC3... vevault-sync
```

---

## Recommendation

| Your setup | Recommendation |
|------------|---------------|
| Tailscale everywhere | **Option 4** — Tailscale SSH. Zero key management. |
| Linux server as central | **Option 3** — systemd timer + passwordless key. Simple, reliable. |
| macOS laptop as remote | **Option 2** — agent socket in cron, or Tailscale SSH. |
| YubiKey user | **Option 5** — gpg-agent. Hardware-backed, secure. |
| Security-conscious | **Option 1** with command restrictions + encrypted key storage (v1.1). |

For most vevault setups, **Tailscale SSH + systemd timer (or launchd on macOS)** is the
simplest path. No key files to manage, no passphrases, and Tailscale handles NAT traversal.

If Tailscale isn't an option, a dedicated passwordless key with command restrictions is
pragmatic — especially since vevault v1.1 will encrypt vaults at rest, adding a second
layer of defense.