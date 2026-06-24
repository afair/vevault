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

- Encryption at rest (gocryptfs-backed FUSE mount)
- Automated backups per vault (restic, post-sync)
- `vv mount` / `vv umount` for encrypted vaults
- `vv backup vault|restore|list` subcommands

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

## Vaults: what they are and how to use them

A **vault** is just a directory. You decide what goes in it. Each vault is an independent
unit of sync, backup, and (in v1.1) encryption — you subscribe hosts to exactly the vaults
they need.

### Real-world example

```
homeserver (central, always-on, holds everything)
├── ~/.local/share/vevault/vaults/
│   ├── personal/          # Documents, photos, notes
│   ├── work/              # Work projects, invoices, contracts
│   ├── dotfiles/          # GNU Stow repo: .bashrc, .gitconfig, nvim/, ssh/config
│   ├── books/             # Calibre library, PDFs, papers
│   └── media/             # Music, videos (large; separate path)
│       → /mnt/data/media  # Living on external storage

laptop
├── subscribed: personal, work, dotfiles
│   (books & media are too big for a laptop SSD)

workstation
├── subscribed: personal, work, dotfiles, books, media
│   (desktop has the disk space for everything)

phone / tablet
├── subscribed: personal, books
│   (only what you access on mobile)
```

### Config for this setup

On the central node:

```bash
# Create vaults — each is its own directory
vv vault create personal
vv vault create work
vv vault create dotfiles
vv vault create books
vv vault create media --path /mnt/data/media

# Laptop: just the essentials
vv subscribe personal  --host laptop
vv subscribe work      --host laptop
vv subscribe dotfiles  --host laptop

# Workstation: everything
vv subscribe personal  --host workstation
vv subscribe work      --host workstation
vv subscribe dotfiles  --host workstation
vv subscribe books     --host workstation
vv subscribe media     --host workstation

# Phone: just personal and books
vv subscribe personal  --host phone
vv subscribe books     --host phone
```

On each remote, subscribe to pull the vaults locally:

```bash
# Laptop
vv init --central homeserver
vv subscribe personal  --symlink ~/Documents/Personal
vv subscribe work      --symlink ~/Work
vv subscribe dotfiles  --symlink ~/dotfiles    # Stow from here

# Workstation
vv init --central homeserver
vv subscribe personal
vv subscribe work
vv subscribe dotfiles
vv subscribe books
vv subscribe media
```

### Common vault patterns

| Vault | What goes in it | Sync to |
|---|---|---|
| **personal** | Documents, photos, notes, scans | Laptop, phone, tablet |
| **work** | Projects, invoices, contracts, tax docs | Laptop, workstation |
| **dotfiles** | GNU Stow repo: shells, editors, git, ssh config | All hosts (every machine benefits) |
| **books** | Calibre library, PDFs, papers, audiobooks | Tablet, e-reader, workstation |
| **media** | Music, videos, large assets | Only hosts with big disks |
| **hosts** | `/etc` backups, cron jobs, systemd units per host | That host + central (backup only) |
| **apps** | App-specific data: password manager vault, RSS reader db | Select hosts |
| **keys** | GPG keys, SSH keys, API tokens (v1.1 encrypted) | Central only, backup only |

### Design tips

- **One concern per vault.** Don't lump photos, work, and configs into one directory. Vaults
  are cheap to create and you get per-vault subscriptions, backup schedules, and encryption.
- **Subscribe sparingly.** Only pull a vault to hosts that need it. Don't sync 200GB of media
  to a laptop with a 256GB SSD.
- **Dotfiles as a vault.** Put your `~/.bashrc`, `~/.config/nvim/`, `~/.ssh/config`, etc.
  in a dotfiles vault managed with [GNU Stow](https://www.gnu.org/software/stow/). Every
  machine subscribes. Change a config on one host, sync, and `stow` on the others.
- **Symlinks for convenience.** `vv subscribe personal --symlink ~/Documents/Personal` keeps
  the vault inside `~/.local/share/vevault/` but gives you a natural access path.
- **Custom paths for large vaults.** `vv vault create media --path /mnt/data/media` puts the
  vault on external storage while keeping it managed by vevault.
- **Profiles for separate lives.** `vv --profile work` gives you an independent vault set
  with its own central node, config, and subscriptions. Use it for work vs personal, or for
  sharing vaults with a partner/friend on a different central.

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
central_host    = "homeserver"
central_address = "100.64.0.5"     # optional: Tailscale/VPN IP
vaults_dir      = "~/vaults"        # ~ expands per host

[[vaults]]
name      = "personal"
symlinks  = ["~/Documents/Personal"]
encryption = false   # v1.1

[[subscriptions]]
host    = "laptop"
address = "laptop.tailnet.ts.net"  # optional: how to reach this host
vaults  = ["personal", "work"]

[subscriptions.paths]
personal = "/Users/allen/vaults/personal"  # optional: macOS path override
```

## License

MIT