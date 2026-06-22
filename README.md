# vevault

Personal file vault management — encrypt, sync, and back up your files across multiple hosts.

A self-hosted replacement for Dropbox, Google Drive, Syncthing, and Nextcloud.
Run it over SSH with the tools you already have.

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

### Central server setup

On your always-on server, NAS, or home server:

```bash
vv init
vv vault create personal
vv vault create work --path /mnt/data/work

# Subscribe other hosts — they'll be populated on next sync
vv subscribe personal --host laptop --host workstation
vv subscribe work --host laptop
```

This creates `~/.local/share/vevault/` with the config, vault storage, and key material.

### Remote client setup

On each laptop, desktop, or other host:

```bash
vv init --central homeserver

# Subscribe this host to vaults — data is pulled immediately
vv subscribe personal --symlink ~/Documents/Personal
vv subscribe work
```

Non-central hosts delegate all sync to central via SSH. No rclone needed here.

### Profiles

Run independent vault sets with `--profile` or `VEVAULT_PROFILE`:

```bash
vv --profile work init --central office-server
vv --profile media init
VEVAULT_PROFILE=work vv vault list
```

Each profile lives in `~/.local/share/<name>/`. The default profile is `vevault`.

### Sync

```bash
# On any host — tell central "I have updates"
vv sync
vv sync personal

# Catch up before going offline (skip propagation to other hosts)
vv sync --pull
```

On central, target a specific host without fan-out:

```bash
vv updates laptop personal -n
```

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

homeserver$ vv updates laptop -n         # Sync only, no propagation
laptop$    vv sync --pull                # Catch-up before going offline
```

- **Two-way sync** with conflict detection — if a file is modified on both sides, both versions are preserved as `.conflict1` / `.conflict2`.
- **Central is the brain** — non-central hosts delegate sync, no version mismatch.
- **Vaults can live anywhere** — inside `~/.local/share/vevault/vaults/` by default, or any custom path.

## Commands

```
vv init                           Bootstrap vevault on this host
vv --profile <name> init          Initialize a named profile
vv vault create <name>            Create a new vault
vv vault delete <name>            Remove a vault
vv vault list                     List all vaults
vv vault info <name>              Show vault details
vv subscribe <vault>              Subscribe hosts to a vault (--host on central)
vv unsubscribe <vault>            Remove a subscription (--purge to delete data)
vv sync [<vault>]                 Sync with central node (--pull for catch-up)
vv updates <host> [<vault>]       Central-only: sync with host [-n to skip fan-out]
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