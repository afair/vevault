# VeVault — Design Document

## 1. Overview

**VeVault** (`vv`) manages a share of file vaults across multiple hosts. A central node holds the
authoritative copy of all vaults. Other hosts subscribe to specific vaults and synchronize
bidirectionally with the central node over SSH. Encryption (at rest) and backup (via restic) are
layered on top, with encryption being a v1.1 feature.

### Design Principles

- **SSH is the transport.** No custom daemon, no open ports. All inter-host communication is
  `ssh user@host vv ...` or rclone over SSH.
- **Central is orchestrator.** The central node performs all 2-way sync. Other hosts only trigger
  syncs ("I have updates, come get them").
- **User owns SSH trust.** `vv` reads host aliases from `~/.ssh/config` and assumes key-based
  auth is already set up.
- **Hybrid encryption.** Data at rest is encrypted internally (Go crypto). Distribution (rclone)
  and backup (restic) use their own encryption layers. The wire is protected by SSH.
- **Single static binary.** Go compiles to one file. No runtime, no pip, no npm. Drop it in
  `$PATH` and go.

---

## 2. Language & Dependencies

### Go 1.22+

| Dependency | Purpose | MVP? |
|---|---|---|
| `github.com/spf13/cobra` | CLI framework, subcommands, completions | Yes |
| `github.com/BurntSushi/toml` | Config file parsing | Yes |
| `golang.org/x/crypto/ssh` | SSH client for remote `vv` invocations | Yes |
| `github.com/pkg/sftp` | SFTP client for ad-hoc file operations | Maybe |
| `github.com/charmbracelet/lipgloss` | Terminal styling, tables, progress | Yes |
| `golang.org/x/crypto/nacl/secretbox` | Authenticated encryption for vault data (v1.1) | No |
| `github.com/hanwen/go-fuse/v2` | FUSE mount for encrypted vaults (v1.1) | No |

For MVP, we shell out to:
- `rclone` (≥1.62) — bidirectional file synchronization via `rclone bisync` (required on all hosts)
- `ssh` — remote command execution (required)
- `restic` — backups (v1.1, optional)

`vv` invokes `rclone` using on-the-fly `:sftp:` backends, so no `rclone.conf` file
management is needed. Rclone can also be bundled with or downloaded by `vv` during
setup if not already installed.

---

## 3. Directory Layout

### Repository Structure

```
vevault/
├── cmd/
│   └── vv/
│       └── main.go              # Entry point
├── internal/
│   ├── config/
│   │   └── config.go            # TOML loading, validation, defaults
│   ├── vault/
│   │   ├── vault.go             # Vault CRUD operations
│   │   └── vault_test.go
│   ├── sync/
│   │   ├── sync.go              # rclone bisync engine
│   │   ├── remote.go            # SSH remote command execution
│   │   └── sync_test.go
│   ├── backup/
│   │   └── backup.go            # Restic wrapper (v1.1)
│   ├── crypto/
│   │   └── crypto.go            # Encrypt/decrypt vault data (v1.1)
│   └── fuse/
│       └── fuse.go              # FUSE filesystem (v1.1)
├── go.mod
├── go.sum
├── Makefile
├── Requirements.md
└── Design.md
```

### Runtime Data Layout

```
~/.local/share/vevault/
├── config.toml              # Main configuration
├── state.db                 # BoltDB: sync timestamps, file manifests
├── vaults/                  # All vault data lives here
│   ├── personal/            #   One directory per vault
│   ├── work/
│   └── books/
├── encrypted/               # Encrypted mirrors (v1.1)
│   ├── personal/            #   gocryptfs-style encrypted tree
│   └── work/
├── keys/                    # Encryption key material (v1.1)
│   └── personal.age         #   One key per vault
└── backups/                 # Backup config cache
    └── personal.restic      #   Restic repo snapshots
```

On vault creation or subscription, the user may request a symlink at an arbitrary path:

```
~/Documents/Personal  ->  ~/.local/share/vevault/vaults/personal
```

These symlinks are tracked in `config.toml` so `vv vault delete` can clean them up.

---

## 4. Configuration Format (TOML)

```toml
# ~/.local/share/vevault/config.toml

[core]
# Which host is the central node (must match an SSH alias in ~/.ssh/config)
central_host = "homeserver"
# Optional: how to reach central (Tailscale IP, VPN, etc.)
central_address = "100.64.0.5"

# Where vault data lives on this host. ~ expands to user home.
vaults_dir = "~/.local/share/vevault/vaults"

# A vault definition
[[vaults]]
name        = "personal"
# Optional: override the default path (default: vaults_dir/name)
path        = "/mnt/data/personal"
# Symlinks to create on this host (created on subscribe, cleaned on delete)
symlinks    = ["~/Documents/Personal"]
# Encryption config (v1.1)
encryption  = false
# Backup config (v1.1)
[backup]
enabled     = false
repo        = "restic:backup-server:personal"
schedule    = "daily"

[[vaults]]
name        = "work"
symlinks    = ["~/Work"]
encryption  = false

# Which remote hosts subscribe to which vaults
# Only relevant on the central node
[[subscriptions]]
host    = "laptop"             # SSH alias
address = "laptop.tailnet.ts.net"  # optional: how to reach this host
vaults  = ["personal", "work"]

[subscriptions.paths]
personal = "/Users/allen/vaults/personal"  # optional: per-host path override

[[subscriptions]]
host  = "workstation"
vaults = ["work"]
```

The config file is synced to all hosts as part of `vv sync --config`, ensuring every host
knows about all vaults and subscriptions. Each host only acts on vaults it is subscribed to.

---

## 5. Data Model

### Vault

A vault is a named directory containing arbitrary files. It is the unit of subscription,
encryption, and backup.

```
Vault {
    Name        string
    Path        string          // Absolute path to the vault directory
    Symlinks    []string        // Symlink targets on this host
    Encryption  *EncryptConfig  // nil if unencrypted (always nil in MVP)
    Backup      *BackupConfig   // nil if not backed up (always nil in MVP)
}
```

### Host

A host is any machine running `vv`. It is identified by its SSH alias as defined in
`~/.ssh/config`.

```
Host {
    Alias       string          // e.g. "laptop", "homeserver"
    Address     string          // optional: how to reach (Tailscale IP, VPN, etc.)
    IsCentral   bool            // true if this is the central node
    Vaults      []string        // Vault names this host is subscribed to
    Paths       map[string]string // optional: per-vault path overrides for this host
}
```

### Subscription

A subscription binds a host to a vault. Stored in central's config, synced to all hosts.

```
Subscription {
    Host    string              // Host alias
    Address string              // optional: how to reach this host from central
    Vaults  []string            // Vault names
    Paths   map[string]string   // optional: per-vault remote path overrides
}
```

The `Paths` map solves cross-platform path differences (e.g. Linux `/home/allen/` vs
macOS `/Users/allen/`). When set, `RemoteVaultPath()` returns the override for rclone
SFTP commands. When absent, the local vault path is used for both sides.

`Address` solves multi-network reachability. A laptop might be `macbook.local` on LAN
but `macbook.tailnet.ts.net` on Tailscale. Central uses `Address` for SFTP; remote
clients use `central_address` for SSH delegation.

### SyncState

Per-vault-per-host sync metadata stored in BoltDB to enable efficient 2-way sync:

```
SyncState {
    Vault       string
    Host        string
    LastSync    time.Time
    LastFileCount int
    LastSize    int64
}
```

---

## 6. Subcommands (MVP)

```
vv vault create <name> [--path <path>] [--symlink <target>]
    Create a new vault directory and register it in config.
    --symlink: Create a symlink at <target> pointing to the vault.

vv vault delete <name> [--yes-im-sure] [--delete-data]
    Remove a vault from config. With --delete-data, remove the directory too.
    Cleans up any tracked symlinks.

vv vault list
    List all vaults known to this host, with subscription status, size,
    and encryption/backup status.

vv vault info <name>
    Show detailed info about one vault: path, size, file count, symlinks,
    subscribed hosts, last sync time.

vv subscribe <vault> [--host <host>] [--symlink <target>]
    Subscribe a host to a vault. Must run on central node.
    --host defaults to the current host if run from the subscribing host
    via SSH. Creates symlinks if requested.

vv unsubscribe <vault> [--host <host>]
    Remove a host's subscription to a vault. Does not delete local data
    on the host unless --purge is passed.

vv sync [<vault>]
    On a non-central host: delegates to central via SSH.
        ssh central vv updates <this-host> [<vault>]
    On central: runs 2-way bisync with the requesting host, then propagates
    to other subscribers.
    If <vault> is given, sync only that vault; otherwise sync all
    subscribed vaults.

vv updates <host> [<vault>]
    Central-only command. Runs rclone bisync with <host>, then propagates
    changes to all other subscribers of the affected vaults.
    This is the command that non-central hosts trigger via SSH.

vv sync --config
    Push the config file and keys/ to all hosts so they have the latest vault
    and subscription definitions. Runs on central.

vv copy clone <vault>[/subdir] <dest>
    Copy a vault (or subdirectory) to a local destination. Respects
    encryption: if the vault is encrypted, decrypt on copy.

vv copy import <vault>[/subdir] <src>
    Copy from a local source into a vault. If encrypted, encrypt on import.
```

---

## 7. Synchronization Algorithm

### 7.1 Execution Model

All sync logic runs **on the central node**. Non-central hosts never run `rclone bisync`
directly. Instead, they delegate to central over SSH:

```
# On laptop (non-central host):
vv sync
  → ssh homeserver vv updates laptop

# Central receives the request and does all the work:
#  1. rclone bisync central ↔ laptop
#  2. rclone bisync central ↔ each other subscriber
```

This has several benefits:
- **No version mismatch**: Only central's `vv` binary needs `rclone` and the full sync
  engine. Non-central hosts can run a minimal `vv` or even a shell wrapper.
- **Single source of truth**: Central owns the sync schedule, conflict resolution, and
  state tracking.
- **Simpler firewalls**: Only central needs outbound SFTP to all hosts. Non-central
  hosts only need outbound SSH to central.

### 7.2 rclone bisync Invocation

The central node uses **rclone bisync** for true bidirectional synchronization:

```
function sync_vault(vault, host):
    rclone bisync                                     \
        central:vaults_dir/vault/                     \
        :sftp:host:vaults_dir/vault/                  \
        --sftp-host=<host> --sftp-user=<user>         \
        --sftp-key-file=<key>                         \
        --create-empty-src-dirs                       \
        --resync                                       # First sync or after interruption

    # Update sync state in BoltDB
```

`rclone bisync` does a single-pass bidirectional sync:
- Compares file listings from both sides (modtime + size by default, optional hash).
- Copies new/changed files to the other side.
- Propagates deletions in both directions.
- Renames are detected and handled without re-transfer.

**Conflict handling:** When the same file is modified on both sides since last sync,
`rclone bisync` preserves **both** versions:
- Side A's version is renamed to `file.txt.conflict1`
- Side B's version is renamed to `file.txt.conflict2`
- The user resolves the conflict manually and on the next sync the resolved file
  replaces both conflict copies.

No data is silently lost.

**First sync / recovery:** `--resync` tells rclone to trust the first path (central) as
authoritative and overwrite the second path. After the initial sync, `--resync` is
omitted for normal bidirectional operation. `--resync` is also used to recover from
interrupted syncs.

### 7.3 Multi-Host Sync Flow

```
Host A runs: vv sync personal

1. Host A → Central (SSH): vv updates laptop personal

2. Central runs rclone bisync with Host A:
   rclone bisync central:vaults/personal/ :sftp:laptop:vaults/personal/
   (2-way sync: both sides get each other's changes)

3. Central runs rclone bisync with each other subscriber:
   For each host H subscribed to "personal" (H != laptop):
       rclone bisync central:vaults/personal/ :sftp:H:vaults/personal/

4. Central updates sync state, records last sync timestamp.

Note: Step 3 is still a full rclone bisync, not a one-way push. This ensures
changes that happened on other hosts (not yet synced) flow back to central and
are then propagated in subsequent syncs.
```

### 7.4 Deletion Handling

`rclone bisync` propagates deletions in both directions by default. If a user deletes a
file on one host and syncs, it is deleted everywhere. This is appropriate for a vault.

For safety:
- `vv vault delete <name> --delete-data` explicitly removes vault data after confirmation.
- `vv sync` never deletes a vault directory itself, only contents.
- `rclone bisync --backup-dir` can optionally move deleted files to a dated backup
  directory instead of removing them. This could be exposed as `vv sync --keep-deleted`
  in the future.

### 7.6 rclone Backend Configuration

VeVault uses rclone's **on-the-fly backend** syntax so users don't need to manage an
`rclone.conf` file:

```
:sftp:host:/path --sftp-host=<host> --sftp-user=<user>
```

The local vault path uses rclone's `local` backend implicitly (just a filesystem path).

VeVault derives SFTP connection parameters from the host's entry in `~/.ssh/config`:

```
Host laptop
    HostName laptop.local
    User allen
    IdentityFile ~/.ssh/id_ed25519
    Port 22
```

Translates to:

```
rclone bisync /local/path :sftp:laptop:/remote/path \
    --sftp-host=laptop.local \
    --sftp-user=allen \
    --sftp-key-file=~/.ssh/id_ed25519 \
    --sftp-port=22
```

This keeps configuration in one place (`~/.ssh/config`) and avoids duplication.

---

## 8. SSH & Remote Execution

### 8.1 Host Addressing

Hosts are referenced by their SSH alias as defined in `~/.ssh/config`:

```
Host homeserver
    HostName 192.168.1.100
    User allen
    IdentityFile ~/.ssh/id_ed25519

Host laptop
    HostName laptop.local
    User allen
```

`vv` resolves "homeserver" to `ssh homeserver` with no additional configuration.

### 8.2 Two-Tier Execution Model

- **Central node** runs the full `vv` Go binary with sync engine, rclone bisync
  orchestration, config management, and state tracking.
- **Non-central hosts** run the same `vv` binary, but sync subcommands detect they
  are not on central and delegate via SSH:

  ```
  # On laptop (non-central host):
  vv sync personal
    → internally runs: ssh homeserver vv updates laptop personal

  vv sync --config
    → internally runs: ssh homeserver vv sync --config
  ```

  Non-central hosts only need SSH outbound to central. They do not run `rclone`
  directly and do not initiate SFTP connections to other hosts.

- **Central never SSHs out to run commands on other hosts.** It uses SFTP (via rclone)
  for all file transfer. Non-central hosts don't need `vv` in `$PATH` for sync to
  work — they only need SSH + SFTP access for central to reach them.

### 8.3 Security Model

- **Authentication:** Via SSH keys. User configures `~/.ssh/config` and key pairs.
  VeVault never handles private keys.
- **Authorization:** Any host with SSH access to central can trigger syncs and read/write
  vaults they subscribe to. Subscription is enforced by the central node checking its
  config before performing syncs.
- **Wire encryption:** Provided by SSH. No additional layer needed.
- **At-rest encryption:** Provided by VeVault's internal crypto (v1.1).

---

## 9. Encryption (v1.1)

### 9.1 Design

Encryption uses NaCl `secretbox` (XSalsa20 + Poly1305) for authenticated encryption of
file contents and filenames.

Each encrypted vault has a 32-byte symmetric key, stored in:
```
~/.local/share/vevault/keys/<vault>.age
```

The key file itself is encrypted with the user's master password (derived via Argon2id).

### 9.2 Encrypted Vault Layout

```
vaults/personal/                   # Plaintext (working directory)
encrypted/personal/                # Encrypted mirror
├── dir_1_<base64(nonce+ciphertext)>/
│   └── file_1_<base64(nonce+ciphertext)>
└── dir_2_<base64(nonce+ciphertext)>/
    └── file_2_<base64(nonce+ciphertext)>
```

Directory and file names are encrypted with a deterministic nonce derived from the
plaintext name. File contents are encrypted with random nonces.

### 9.3 Operations

| Operation | Behavior |
|---|---|
| `vv vault create --encrypt` | Generate key, create both plaintext and encrypted dirs |
| Write to `vaults/<name>/` | On `vv sync --encrypt`, encrypt to `encrypted/<name>/` |
| `vv sync` (encrypted vault) | Sync the encrypted tree, decrypt on receiving host |
| `vv mount <name> <dir>` | FUSE mount: `encrypted/<name>/` exposed as plaintext at `<dir>` |
| `vv copy clone <name> <dest>` | If encrypted, decrypt on copy to `<dest>` |

### 9.4 Key Distribution

Encryption keys are synced between hosts as part of `vv sync --config` (the `keys/`
directory is included in config distribution). The master password must be set on each
host to decrypt the key files.

---

## 10. Backup (v1.1)

### 10.1 Design

Backups are per-vault, using restic repositories. Configuration:

```toml
[[vaults]]
name = "personal"
[backup]
enabled   = true
repo      = "sftp:backup-server:/backups/personal"
password  = "${RESTIC_PASSWORD}"   # Env var reference
schedule  = "daily"
retention = { daily = 7, weekly = 4, monthly = 6 }
```

### 10.2 Backup Flow

```
vv backup all
  → For each vault with backup enabled:
       restic -r <repo> backup <vault_path>
       restic -r <repo> forget --prune <retention_policy>
  → Backup the vevault config + keys:
       restic -r <config_repo> backup ~/.local/share/vevault/

vv backup vault <name>
  → Backup a single vault to its configured repo

vv backup restore <name> <target>
  → restic -r <repo> restore latest --target <target>
```

### 10.3 3-2-1 Strategy

Multiple backup destinations per vault:

```toml
[[vaults]]
name = "personal"
[[backups]]
repo   = "sftp:nas:/backups/personal"     # Local NAS
schedule = "daily"
[[backups]]
repo   = "s3:s3.amazonaws.com/bucket"     # Offsite
schedule = "weekly"
```

---

## 11. Platforms & Distribution

### 11.1 Build Targets

| OS | Arch | Notes |
|---|---|---|
| Linux | amd64, arm64 | Primary target (includes Raspberry Pi) |
| macOS | amd64, arm64 | Native Darwin binaries |
| FreeBSD | amd64 | Via Go cross-compilation |
| Windows | amd64 | WSL only for MVP; native Windows possible but de-prioritized |

### 11.2 Installation

```bash
# One-liner install (future)
curl -sSL https://vevault.dev/install.sh | bash

# Manual
wget https://github.com/user/vevault/releases/latest/vv-linux-amd64
chmod +x vv-linux-amd64
sudo mv vv-linux-amd64 /usr/local/bin/vv
```

### 11.3 Shell Completions

Generated via cobra: `vv completion bash|zsh|fish > ...`

---

## 12. Error Handling & Edge Cases

| Scenario | Behavior |
|---|---|
| Sync during active file writes | rclone copies whole files; partial writes result in the pre-write version on the other side. Next sync propagates the complete file. |
| Host offline during sync | Central skips unreachable hosts, logs warning, retries next sync |
| Concurrent syncs from two hosts | Central serializes: first sync completes, second sees updated state |
| Disk full on sync | rclone fails with error; `vv` reports it. `--resync` used on next attempt to recover cleanly. |
| Vault deleted on central, still on host | Next `rclone bisync` propagates deletion. Host warned on `vv sync` |
| Encryption key lost | Data unrecoverable. Mitigated by backup of `keys/` directory |
| Symlink target already exists | `vv` errors with `--force` flag to overwrite |

---

## 13. MVP Scope (v0.1)

### Included

- [x] `vv vault create|delete|list|info`
- [x] `vv subscribe|unsubscribe`
- [x] `vv sync [<vault>]`
- [x] `vv updates <host> [<vault>]` (central-only)
- [x] `vv sync --config`
- [x] `vv copy clone|import`
- [x] TOML config, BoltDB state
- [x] 2-way sync via rclone bisync
- [x] SSH-based remote execution
- [x] Symlink management
- [x] Shell completions (bash, zsh, fish)

### Deferred to v1.1

- [ ] Encryption (NaCl secretbox + FUSE mount)
- [ ] Backup (restic integration)
- [ ] `vv mount|umount` (FUSE)
- [ ] `vv ln` (as distinct from symlinks on create/subscribe)
- [ ] Compression

### Deferred to v2.0+

- [ ] Native Windows support
- [ ] Push notifications (inotify-based auto-sync)
- [ ] Automatic conflict resolution strategies (merge tools, rules)
- [ ] `.vevault-trash/` for soft deletes
- [ ] Web UI / TUI dashboard
- [ ] API key auth as SSH alternative

---

## 14. Resolved Questions

1. **Sync delegation — RESOLVED.** Non-central hosts delegate sync to central via
   `ssh central vv updates <host> [<vault>]`. Central runs all `rclone bisync` logic.
   This avoids version mismatch: only central needs the full sync engine and `rclone`.
   Non-central hosts can run the same `vv` binary (it detects non-central and delegates)
   or a thin shell wrapper. See §7.1 and §8.2.

2. **rclone vs. rsync — RESOLVED.** Using `rclone bisync` for its conflict detection
   and single-command bidirectional sync. See §7.

3. **Subscription acceptance — RESOLVED.** No additional accept step. Central config is
   authoritative. A new subscription triggers a `--resync` on the next sync, which
   copies central's data to the subscribing host.

4. **Symlinks inside vaults — RESOLVED.** `rclone bisync --links` preserves symlinks as
   symlinks. A symlink to `/home/user/bigfile` inside a vault remains a symlink across
   hosts, even if the target path only exists on one host.

5. **Config sync conflicts — RESOLVED for MVP.** Since sync is manually triggered (no
   cron, no auto-sync in MVP), there are no concurrent config edits. Two users won't
   race to edit and push config. If this changes (e.g., Docker image generation, CI
   pipelines), a `vv config edit` subcommand with file locking should be added.
   Deferred.