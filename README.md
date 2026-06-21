# vevault

Personal file vault management — encrypt, sync, and back up your files across multiple hosts.

**`vv`** is a single static binary. No daemon, no open ports. All communication runs over SSH, all sync uses `rclone bisync`, and all backups use `restic`. Central node orchestrates; other hosts subscribe.

## Quick start

### Install

```bash
# Build from source (requires Go 1.22+)
git clone https://github.com/allen/vevault.git
cd vevault
make build
sudo make install        # → /usr/local/bin/vv
```

Prebuilt binaries coming soon.

### Initialize

```bash
# On your central node (always-on server, NAS, home server):
vv init

# On each other host:
vv init --central homeserver
```

This creates `~/.local/share/vevault/` with the config, vault storage, and key material.

### Create your first vault

```bash
vv vault create personal
vv vault create work --path /mnt/data/work    # Vaults can live anywhere
vv vault create dotfiles --symlink ~/dotfiles
```

### Subscribe hosts

On the central node, subscribe other hosts to vaults:

```bash
vv subscribe personal --host laptop
vv subscribe work --host laptop --host workstation
```

### Sync

```bash
vv sync                 # Tell central "I have updates" — central pulls, then propagates
vv sync personal        # Sync a specific vault
```

On a non-central host, `vv sync` delegates to central via SSH. All sync logic runs on central.

### Coming in v1.1

- Encryption at rest (NaCl secretbox + FUSE mount)
- Automated backups per vault (restic)
- `vv mount` / `vv umount` for encrypted vaults

## How it works

```
laptop$ vv sync personal
  → ssh homeserver vv updates laptop personal

homeserver$ vv updates laptop personal
  → rclone bisync central ↔ laptop       # 2-way sync
  → rclone bisync central ↔ workstation  # propagate to subscribers
```

- **Two-way sync** with conflict detection — if a file is modified on both sides, both versions are preserved as `.conflict1` / `.conflict2`.
- **Central is the brain** — non-central hosts delegate sync, no version mismatch.
- **Vaults can live anywhere** — inside `~/.local/share/vevault/vaults/` by default, or any custom path.

## Commands

```
vv init                        Bootstrap vevault on this host
vv vault create <name>         Create a new vault
vv vault delete <name>         Remove a vault
vv vault list                  List all vaults
vv vault info <name>           Show vault details
vv sync [<vault>]              Sync with central node
vv updates <host> [<vault>]    Central-only: sync with host + propagate
```

## Requirements

- **Go 1.22+** (build only — binary has no runtime deps)
- **rclone ≥ 1.62** (for `bisync`) on the central node
- **SSH** with key-based auth between all hosts
- **restic** (optional, for v1.1 backups)

## Config

`~/.local/share/vevault/config.toml`:

```toml
[core]
central_host = "homeserver"
vaults_dir   = "~/.local/share/vevault/vaults"

[[vaults]]
name      = "personal"
symlinks  = ["~/Documents/Personal"]
encryption = false   # v1.1

[[subscriptions]]
host   = "laptop"
vaults = ["personal", "work"]
```

## License

MIT