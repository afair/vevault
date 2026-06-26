# VeVault Encryption Design

> **Status:** v1.1 — design clarification & implementation plan.
> **Relevant:** [Design §9](Design.md), [Requirements §Encryption](Requirements.md), [Backup](backup.md)

---

## 1. Decided: gocryptfs

**gocryptfs is the encryption layer for all vault file data.** Every file and filename
in an encrypted vault is encrypted by gocryptfs. `vv` does not do file-level encryption —
it manages the lifecycle around gocryptfs: key generation, key storage, mount/unmount,
and key distribution across hosts.

**What `vv` handles vs what gocryptfs handles:**

| Layer | Tool | Scope |
|---|---|---|
| File contents | gocryptfs | Every read/write through the FUSE mount is transparently encrypted/decrypted |
| Filenames | gocryptfs | Directory listings show encrypted names on disk, plaintext through the mount |
| Directory structure | gocryptfs | Directory names encrypted; `gocryptfs.diriv` per directory for IV management |
| Vault key storage | `vv` (NaCl secretbox) | The gocryptfs password (32-byte key) wrapped with the master password in `keys/<name>` |
| Master password derivation | `vv` (Argon2id) | Human password → 32-byte master key |
| Key distribution | `vv` (`sync --config`) | `keys/<name>` pushed to all subscribed hosts |
| Mount/unmount lifecycle | `vv` | Invokes gocryptfs, passes key securely via `-passfile /dev/fd/N` |

### Quick Answers

**Q: Is all encryption done by gocryptfs?**
Yes — all file contents, filenames, and directory structure. `vv` only handles the
key wrapping (encrypting the gocryptfs password with the master password, using NaCl
secretbox) and distributing keys to other hosts. `vv` never touches file encryption.

**Q: How do remote hosts decrypt vaults?**
The vault key (gocryptfs password) is stored in `keys/<name>`, encrypted with the
master key. `vv sync --config` distributes `keys/<name>` + the Argon2id salt to all
subscribed hosts. Each host uses the same master password (typed once via `vv key set`)
to decrypt the vault key. Then gocryptfs uses that key to mount the vault.

**Q: Is a central master password required?**
Yes — one master password shared across all hosts the user owns. This is the simplest
model: one user, one password, multiple devices. The encrypted key files are synced
to all hosts anyway, so using the same password doesn't change the threat surface.

**Q: What platforms don't support gocryptfs/FUSE?**
Windows natively: ❌ (use WSL2). FreeBSD: ⚠️ untested, likely needs patches.
Non-encrypted vaults work everywhere. A host without gocryptfs can still sync, backup,
and restore encrypted vaults — it just can't mount them locally. See §12 for full matrix.

**Q: Does vevault work without encrypted vaults?**
Yes. Encryption is per-vault (`--encrypt` flag on create). Non-encrypted vaults work
identically to the current MVP — no gocryptfs required. You can mix encrypted and
unencrypted vaults in the same vevault instance.

---

## 2. The Confusion: Why Two Directories?

The original Design.md §9.2 proposed this layout:

```
vaults/personal/           # Plaintext (working directory)
encrypted/personal/        # Encrypted mirror
```

This is **wrong** and we're fixing it. The dual-directory model has several problems:

| Problem | Why |
|---|---|
| **Not "at rest"** encryption | Plaintext is always on disk in `vaults/`. The encrypted mirror is redundant, not protective. |
| **Double disk usage** | Every file exists twice. A 50GB vault becomes 100GB. |
| **Consistency risk** | Which is authoritative — plaintext or encrypted? What if they diverge? |
| **Mental model mismatch** | Users expect "encrypted vault" to mean the data is encrypted on disk. This design encrypts for transport, not for rest. |
| **Sync confusion** | Which directory does `vv sync` operate on? The design says "sync the encrypted tree" — but then how do you work with files? |

The dual-directory design treats encryption as a **sync-time transformation**: encrypt before sync,
decrypt after sync. That's transport encryption, not at-rest encryption. SSH already covers
transport. The whole point of vault encryption is protecting data when the host is **offline,
stolen, or compromised** — which means the at-rest copy must be ciphertext.

### What "Encryption at Rest" Actually Means

```
┌──────────────────────────────────────────────────────────┐
│  Disk (unmounted):                                       │
│    vaults/personal/                                      │
│      dir_A1B2C3/          ← encrypted directory name     │
│        file_X7Y8Z9        ← encrypted file name          │
│        [ciphertext]       ← encrypted content            │
│                                                          │
│  After vv mount personal ~/Personal:                     │
│    ~/Personal/              ← FUSE plaintext view        │
│      dir/                   ← decrypted name             │
│        file.txt             ← decrypted name             │
│        [hello world]        ← decrypted on read          │
└──────────────────────────────────────────────────────────┘
```

**One directory on disk — ciphertext.** FUSE provides a plaintext view when you need to work.
When unmounted, only ciphertext exists. That's at-rest encryption.

---

## 3. The Corrected Model

### 2.1 Directory Layout

```
~/.local/share/vevault/
├── config.toml
├── state.db
├── vaults/                  # All vault data — ciphertext on disk
│   ├── personal/            # Encrypted vault (single source of truth)
│   └── work/
├── keys/                    # Encryption key material
│   ├── personal.age         # Per-vault symmetric key, encrypted with master password
│   └── work.age
└── mount/                   # Symlink farm or empty dir for active mounts (optional)
```

No `encrypted/` mirror. The vault **is** the ciphertext.

### 3.2 Key Hierarchy (One Password, All Hosts)

```
Master Password (human-memorized, same password on every host)
       │
       │  Argon2id + salt (salt synced via config sync)
       ▼
Master Key (32 bytes; derived, never written to disk)
       │
       │  NaCl secretbox decrypt
       ▼
Vault Key (32 bytes, stored in keys/<vault>.age, synced to all hosts)
       │
       │  Passed to gocryptfs via -passfile
       ▼
Vault Data (files + filenames + directory structure — gocryptfs handles everything)
```

The encrypted `keys/<name>` file is identical on every host. Any host with the
master password can decrypt it. No per-host key wrapping needed.

### 2.3 Access Modes

```
                    ┌──────────────────┐
                    │  vaults/personal/ │  ← ciphertext on disk (always)
                    └────────┬─────────┘
                             │
          ┌──────────────────┼──────────────────┐
          ▼                  ▼                  ▼
   ┌─────────────┐   ┌─────────────┐   ┌──────────────┐
   │ FUSE mount  │   │ vv copy     │   │ vv sync      │
   │ (daily use) │   │ clone/import│   │ vv backup    │
   └─────────────┘   └─────────────┘   └──────────────┘
   Plaintext view    One-shot decrypt/   Operate on
   at ~/Personal     encrypt to target   ciphertext
```

- **FUSE mount** (`vv mount`): The primary way to work with an encrypted vault. Mounts the
  ciphertext directory as a plaintext filesystem. Read/write/create/delete work normally.
  No separate plaintext copy — FUSE decrypts on read, encrypts on write, transparently.

- **Copy in/out** (`vv copy clone|import`): For one-shot operations. Decrypt a snapshot to
  a target directory, or encrypt files into the vault. No mount required.

- **Sync & backup**: Both operate on the **ciphertext** directory directly. Central syncs
  encrypted blobs. Backup stores encrypted blobs (plus a separate config/keys backup so you
  can decrypt later).

---

## 4. FUSE: gocryptfs vs Internal Implementation

This is the critical architectural decision. We have two options:

### Option A: Internal (go-fuse + NaCl secretbox) — Current Design.md Plan

```
vv mount personal ~/Personal
  └─→ Go FUSE server (hanwen/go-fuse v2)
       decrypts on read, encrypts on write
       using NaCl secretbox per file
```

| Pros | Cons |
|---|---|
| Single binary — no external deps beyond rclone, restic | **Building encrypted FS from scratch is hard and dangerous** |
| Full control over encryption params | Filename encryption requires deterministic nonces (subtle) |
| Go-native, cross-platform FUSE | Directory structure obfuscation is complex |
| Already in Design.md dependency table | IV/nonce reuse = catastrophic failure |
| | No existing audit; we become the security surface |
| | Concurrent access: file locking, atomic rename semantics |
| | Padding to hide file sizes |
| | Hard link / symlink handling |
| | xattr preservation |
| | Every filesystem operation is a crypto risk surface |
| | macOS FUSE (macFUSE) has rough edges |

### Option B: gocryptfs (External, Like rclone)

```
vv mount personal ~/Personal
  └─→ gocryptfs vaults/personal/ ~/Personal/
       (vv manages the config + key)
```

| Pros | Cons |
|---|---|
| **Battle-tested, audited** (2017 academic audit, ongoing community scrutiny) | External dependency (but so is rclone, restic, ssh) |
| Filename encryption (AES-EME) is solved | Must be installed separately |
| Reverse mode for encrypted read-only view | `.gocryptfs.conf` file in vault dir (but we own it) |
| Handles all edge cases (concurrency, xattrs, hardlinks) | Not Go-native; can't embed easily |
| Active maintenance, backward compatibility | Config format is gocryptfs-specific |
| Familiar to users who already use it | |
| XChaCha20-Poly1305 support (modern cipher) | |
| We don't build crypto — we integrate crypto | |

### Option C: rclone crypt (Overlay, No FUSE)

```
vv vault create personal --encrypt
  └─→ Configure rclone crypt remote for this vault
      Files stored as ciphertext via rclone overlay
      vv copy clone personal ~/out/  → rclone copy :crypt:personal ~/out/
```

| Pros | Cons |
|---|---|
| Reuses rclone (already a dependency) | No FUSE — no transparent daily use |
| rclone crypt is mature | Must use `vv copy clone|import` for every file operation |
| Same encryption across sync and local storage | Awkward workflow for frequent edits |
| | Ciphertext layout is rclone-specific |

---

### Recommendation: Option B — gocryptfs, Like We Already Use rclone

**Rationale:**

1. **Precedent in the project:** We shell out to `rclone` for sync and `restic` for backup.
   We don't reimplement file synchronization or incremental backup from scratch. We shouldn't
   reimplement encrypted filesystems from scratch either. The "single binary" design principle
   refers to `vv` itself — the Go binary. External tools for specialized work (rclone, restic,
   ssh) are already accepted as runtime dependencies.

2. **Security:** Encryption is the worst place to be adventurous. gocryptfs has been
   professionally audited. An internal implementation would be a 30,000-line crypto project
   that would need its own audit before anyone should trust it.

3. **Filename encryption is genuinely hard.** You need a deterministic, length-preserving,
   collision-resistant encryption for filenames that doesn't leak directory structure.
   gocryptfs uses AES-EME (ECB-Mix-ECB, a wide-block cipher mode) for this. Getting this
   right from scratch is months of work plus an audit.

4. **FUSE edge cases:** Locking semantics, `mmap`, `O_DIRECT`, rename races, `fsync` guarantees,
   directory caching, inode number stability, hard links across directories — gocryptfs has
   solved all of these. We'd have to rediscover each one through bug reports.

5. **We can still reduce the dependency burden:**
   - Ship a statically-linked gocryptfs binary with `vv` (or download it like `rclone`)
   - `vv init` checks for gocryptfs and offers to download it
   - `vv mount` execs gocryptfs with a managed config, so the user never touches it directly

**What changes in the dependency table:**

| Dependency | Purpose | Change |
|---|---|---|
| ~~`golang.org/x/crypto/nacl/secretbox`~~ | ~~FUSE-level file encryption~~ | Remove |
| ~~`github.com/hanwen/go-fuse/v2`~~ | ~~FUSE mount~~ | Remove |
| `gocryptfs` (≥2.0) | Encrypted FUSE mount | Add (shell out, like rclone) |
| `golang.org/x/crypto/argon2` | Master password → master key derivation | Add (lightweight, no audit risk) |
| `golang.org/x/crypto/nacl/secretbox` | Key file encryption (keys/<vault>.age) | Keep (small scope, low risk) |

The NaCl secretbox dependency stays but its scope shrinks dramatically: it only encrypts
the **key files** (32-byte keys, single operation, no streaming, no filenames, no FUSE).
This is a trivial, safe use of secretbox.

---

## 5. How It Works — Concrete Example

### 4.1 Create an Encrypted Vault

```
central$ vv vault create personal --encrypt
```

What happens:
1. Generate 32-byte random vault key.
2. Write `keys/personal` (vault key, encrypted with master key via NaCl secretbox).
3. Create `vaults/personal/` directory.
4. Generate `vaults/personal/.gocryptfs.conf` (gocryptfs config, with `-scryptn 16`
   for key derivation, pointing at the vault key passed via `-passfile`).
5. Initialize the gocryptfs filesystem (`gocryptfs -init -passfile <keyfd> vaults/personal/`).
6. Update `config.toml` with `encryption = true`.

The vault key is stored in two forms:
- `keys/personal` — encrypted with master password (for cross-host key distribution)
- Internally managed via `-passfile` for gocryptfs mounts (runtime only)

### 4.2 Mount and Work

```
central$ vv mount personal ~/Personal
# → gocryptfs vaults/personal/ ~/Personal/ -passfile /proc/self/fd/N
#   (password piped via fd, never on command line)

central$ echo "hello" > ~/Personal/notes.txt
central$ ls ~/Personal/
notes.txt

central$ ls vaults/personal/
aLoNgBaSe64EnCoDeDnAmE          # encrypted filename
dir_A1B2C3D4/                    # encrypted dir name
.gocryptfs.conf                  # gocryptfs config
gocryptfs.diriv                  # per-directory IVs
```

Work with files normally. gocryptfs handles encrypt/decrypt transparently.

### 4.3 Sync (While Mounted or Unmounted)

```
central$ vv sync personal
  → rclone bisync vaults/personal/ :sftp:laptop:vaults/personal/
```

Sync operates on ciphertext. Whether the vault is mounted or not doesn't matter —
gocryptfs handles concurrent access safely, and rclone reads the ciphertext files
from disk. The receiving host gets the same ciphertext tree + config + key.

### 4.4 Unmount

```
central$ vv umount personal
# → fusermount -u ~/Personal   (or umount on macOS)
```

After unmount, `~/Personal` is empty/removed. Only ciphertext remains on disk.

### 4.5 Backup

```
central$ vv backup vault personal
  → restic backup vaults/personal/    (ciphertext)
  → restic backup keys/personal       (encrypted vault key)
```

Backups store ciphertext + the encrypted vault key. To restore: recover the key
(with master password), recover the ciphertext, mount, done.

### 4.6 Copy In/Out (No Mount Required)

```
# Decrypt one file to stdout
central$ vv copy clone personal/notes.txt ~/export/
  → gocryptfs -ro vaults/personal/ /tmp/vv-mount-XXXXX/
  → cp /tmp/vv-mount-XXXXX/notes.txt ~/export/
  → umount /tmp/vv-mount-XXXXX/

# Encrypt a file into the vault
central$ vv copy import personal/notes.txt ~/import/new-notes.txt
  → gocryptfs vaults/personal/ /tmp/vv-mount-XXXXX/
  → cp ~/import/new-notes.txt /tmp/vv-mount-XXXXX/notes.txt
  → umount /tmp/vv-mount-XXXXX/
```

For one-shot operations, `vv copy` does a quick mount → copy → unmount behind the
scenes. The user never sees the temp mount.

---

## 6. Master Password & Cross-Host Key Distribution

### 6.1 The Core Question: One Password or Many?

A remote host receives the ciphertext vault via sync. To mount it, it needs the
vault key. The vault key is stored in `keys/<name>`, encrypted with the master key.
The master key is derived from the master password + salt.

**If every host had its own master password**, `keys/<name>` couldn't be decrypted
except on the host that encrypted it. We'd need per-host key wrapping — encrypt
`keys/<name>` separately for each host's password. That's complex and fragile.

**Decision: One shared master password across all hosts.**

```
                    ┌──────────────────────────────┐
                    │     Master Password          │
                    │   (one, shared across all    │
                    │    hosts the user owns)      │
                    └────────────┬─────────────────┘
                                 │
                    Argon2id + salt (synced)
                                 │
                    ┌────────────▼─────────────────┐
                    │     Master Key (32 bytes)    │
                    │   (derived, never stored)    │
                    └────────────┬─────────────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │                  │                  │
    NaCl secretbox      NaCl secretbox      NaCl secretbox
    decrypt             decrypt             decrypt
              │                  │                  │
    ┌─────────▼─────┐  ┌────────▼──────┐  ┌───────▼───────┐
    │ keys/personal │  │  keys/work    │  │ keys/media    │
    │ (vault key,   │  │  (vault key,  │  │ (vault key,   │
    │  encrypted)   │  │   encrypted)  │  │  encrypted)   │
    └───────┬───────┘  └───────┬───────┘  └───────┬───────┘
            │                  │                  │
            ▼                  ▼                  ▼
      gocryptfs           gocryptfs           gocryptfs
      mount               mount               mount
```

This is the simplest model that works and it matches reality: one user, one password,
multiple devices. The encrypted key files are already synced to all hosts anyway —
having the same password doesn't change the threat surface.

**Setup flow:**

```
central$ vv key set                              # Set the master password once
  Enter master password: ********
  Confirm:                ********
  → Derives master key, stores salt in keys/.master.salt
  → Caches master key in system keyring for this session

central$ vv vault create personal --encrypt      # Creates vault, generates vault key,
                                                   encrypts it with master key → keys/personal

central$ vv sync --config                         # Pushes config + keys + salt to all hosts

laptop$ vv key set                                # Same password on the laptop
  Enter master password: ********                 # (user types the same password)
  → Same salt + same password = same master key
  → Can now decrypt keys/personal → mount the vault

laptop$ vv mount personal ~/Personal              # Works — vault key decrypted with master key
```

### 6.2 What sync --config Distributes

When `vv sync --config` runs on central, it pushes to all subscribed hosts:

| Item | Purpose |
|---|---|
| `config.toml` | Vault definitions, subscriptions |
| `keys/<name>` | Encrypted vault key (gocryptfs password) |
| `keys/.master.salt` | Argon2id salt for master key derivation |

**What's NOT distributed:** the master password itself. The user types it on each host.

### 6.3 Keyring Integration

To avoid re-typing the master password for every mount/sync:

| Platform | Backend |
|---|---|
| Linux (GNOME/KDE) | freedesktop Secret Service |
| Linux (headless) | File in `~/.local/share/vevault/keys/.session` (0600) with TTL |
| macOS | macOS Keychain |

`vv mount personal` checks the keyring first. Falls back to password prompt if
expired or missing. `vv key forget` clears it.

---

## 7. Sync Considerations

### 6.1 What Gets Synced

| Path | Synced? | Notes |
|---|---|---|
| `vaults/<name>/` (ciphertext) | ✅ Yes | Via rclone bisync. This IS the vault. |
| `keys/<name>` (encrypted vault key) | ✅ Yes | Via `vv sync --config`. Needed for mount on remote hosts. |
| `.gocryptfs.conf` | ✅ Yes | Part of the ciphertext dir. Synced automatically with vault data. |
| `gocryptfs.diriv` | ✅ Yes | Per-directory IV files. Also part of the ciphertext dir. |
| Mount points (`~/Personal`) | ❌ No | Local-only. Managed by `vv mount` on each host. |

### 6.2 Encryption + Sync Workflow

```
laptop$ vv mount personal ~/Personal
laptop$ # edit files in ~/Personal...
laptop$ vv sync personal
  → ssh central vv updates laptop personal
  → central bisyncs ciphertext with laptop
  → central propagates ciphertext to other subscribers
  → on each receiving host: ciphertext is updated on disk
  → if the vault is mounted on a receiving host: gocryptfs
    sees new ciphertext files and serves them as plaintext on
    next read. No manual intervention needed.
```

This works because gocryptfs and rclone operate on different layers:
- rclone syncs ciphertext files (opaque blobs).
- gocryptfs intercepts filesystem calls and encrypts/decrypts.

No coordination needed. No locking issues. Just file-level reads and writes.

### 6.3 Race: Mount + Sync + Write

What if a vault is mounted on two hosts and both write to the same file before syncing?
Same conflict resolution as unencrypted vaults — rclone bisync produces `.conflict1` /
`.conflict2` files. These are ciphertext files with conflict marker names; gocryptfs
will show them as encrypted filenames. We should exclude `*.conflict*` from the
`.vvignore` (already done in sync.go) so they propagate normally and the user can
resolve them.

---

## 8. Backup Considerations

This is the critical cross-over with `backup.md`. The encryption model directly
affects backup design.

### 7.1 What to Back Up

| Item | Contents | Why |
|---|---|---|
| `vaults/<name>/` | Ciphertext vault data | This is the vault. Backed up as-is. |
| `keys/<name>` | Vault key (encrypted with master password) | Without this, ciphertext is unrecoverable. |
| `keys/.master.salt` | Argon2id salt | Needed to derive master key from password. |
| `config.toml` | Vault definitions | Needed to know which vaults exist + their paths. |

### 7.2 Which Layer Is "The Backup Encryption"?

There are three encryption layers potentially in play:

```
Layer 1: gocryptfs          → vaults/personal/ is ciphertext on disk
Layer 2: restic             → backup repo is encrypted (restic's built-in)
Layer 3: SSH/rclone TLS     → transport encryption (ephemeral)
```

**Backup flow:**
1. restic reads ciphertext from `vaults/personal/` (already gocryptfs-encrypted).
2. restic encrypts it again with the repository password before writing to the
   backup destination.
3. The backup destination stores double-encrypted blobs.
4. `keys/<name>` is backed up in a separate snapshot (or same repo, different tag).

**Is double encryption wasteful?** Slightly, but it's the right call:
- gocryptfs: protects data at rest on the vault host.
- restic: protects data at rest on the backup destination. Different threat models.
- If the backup repo is compromised (restic password stolen), the data is still
  gocryptfs-encrypted. Defense in depth.
- If we backed up plaintext (FUSE-mounted view) instead, a compromised backup
  would expose everything immediately.

The small CPU overhead of double encryption is negligible for the vast majority
of files. Deduplication still works because restic deduplicates before encryption.

### 7.3 Restore Procedure

```
# Full recovery after total loss:

1. Install vv + deps on new host
2. Set master password:      vv key set   (same password as before)
3. Restore config + keys:    vv backup config restore ~/.local/share/vevault/
4. Restore vault data:       vv backup restore personal ~/.local/share/vevault/vaults/
5. Mount:                    vv mount personal ~/Personal
6. Verify:                   ls ~/Personal/
```

The keys restore includes `keys/personal` (encrypted vault key) and
`keys/.master.salt`. With the master password, `vv key set` derives the master key,
decrypts the vault key, and gocryptfs can mount the ciphertext.

---

## 9. Config Design

```toml
[[vaults]]
name       = "personal"
encryption = true
# gocryptfs config is auto-generated in vaults/personal/.gocryptfs.conf
# No per-vault encryption settings needed — it's a bool.

# Optional: use a different cipher for this vault
[encryption]
cipher = "xchacha20-poly1305"   # default; also "aes-gcm" for faster/no hardware
ko    = "argon2id"              # key derivation for gocryptfs master key
scryptn = 16                    # scrypt hardness (gocryptfs init param)
```

The `encryption = true` flag is sufficient for most users. Advanced cipher selection
is optional.

---

## 10. CLI Design

```
vv key set                         Set the master password for this host
vv key status                      Check if master key is in keyring
vv key forget                      Remove master key from keyring

vv mount <vault> <dir>             Mount encrypted vault as plaintext at <dir>
    --ro                            Read-only mount
    --no-keyring                    Prompt for password even if in keyring

vv umount <vault>                  Unmount an encrypted vault
vv umount --all                    Unmount all

vv vault create <name> --encrypt   Create an encrypted vault
    --cipher <name>                 Choose cipher (default: xchacha20-poly1305)

vv copy clone <vault>[/path] <dest>
    Copy from vault (decrypting) to plaintext <dest>
    If vault is encrypted: mounts → copies → unmounts transparently.
    If vault is not encrypted: simple file copy.

vv copy import <vault>[/path] <src>
    Copy from plaintext <src> into vault (encrypting)
    Same transparent mount → copy → unmount for encrypted vaults.
```

### Commands That Don't Change

```
vv sync [<vault>]         Operates on ciphertext. No change.
vv backup vault <name>    Backs up ciphertext + keys. No change.
vv vault list             Shows encryption status column.
vv vault info <name>      Shows cipher, key status, mount state.
```

---

## 11. Implementation Plan

### Phase 1: Key Management (self-contained, no FUSE)

1. **`internal/crypto/`** — Keep NaCl secretbox for key file encryption.
   - `GenerateVaultKey() ([]byte, error)` — 32 random bytes.
   - `EncryptKeyFile(vaultKey, masterKey []byte) ([]byte, error)` — secretbox seal.
   - `DecryptKeyFile(encrypted, masterKey []byte) ([]byte, error)` — secretbox open.
   - `DeriveMasterKey(password string, salt []byte) ([]byte, error)` — Argon2id.

2. **`vv key set` command** — Prompt for master password, derive master key,
   store in system keyring. Write `.master.salt`.

3. **`vv key status` / `vv key forget`** — Keyring management.

4. **Extend `vv vault create`** — Add `--encrypt` flag. Generate vault key,
   encrypt it with master key, store in `keys/<name>`.

### Phase 2: gocryptfs Integration

5. **gocryptfs detection** — Check for `gocryptfs` in PATH at `vv init` time.
   Offer to download/symlink if missing.

6. **`vv vault create --encrypt` gocryptfs init:**
   - Generate gocryptfs config via `gocryptfs -init -xchacha -scryptn 16`.
   - Pass vault key as password (via pipe, not command line).
   - Store `.gocryptfs.conf` inside `vaults/<name>/`.

7. **`vv mount <vault> <dir>`:**
   - Resolve vault key (keyring → prompt).
   - `exec gocryptfs vaults/<name>/ <dir> -passfile /dev/fd/N` (key piped in).
   - Options: `-ro` for read-only, `-nosyslog` to keep output clean.

8. **`vv umount <vault>`:**
   - Resolve mount point from config / tracking.
   - `fusermount -u <dir>` (Linux) / `umount <dir>` (macOS).

### Phase 3: Copy & Sync Integration

9. **`vv copy clone <vault>[/path] <dest>`**: Detect encryption flag, temporary
   FUSE mount, file copy, unmount.

10. **`vv copy import <vault>[/path] <src>`**: Same pattern in reverse.

11. **`vv sync`**: No changes needed — sync already operates on `vaults/<name>/`
    which is now ciphertext. Verified by Phase 2 tests.

12. **`vv backup`**: No changes needed. The ciphertext is simply a directory
    of files. `restic backup` doesn't care. Key backup is separate (already
    in backup plan).

### Phase 4: Polish

13. **`vv vault info`** — Show encryption cipher, key status, mount state.
14. **`vv vault list`** — Add encryption status column (🔒).
15. **Config sync integration** — `vv sync --config` pushes keys to all hosts.
16. **systemd mount unit generation** — `vv mount --automount` generates a
    systemd `.mount` unit for auto-mount at boot.

---

## 12. Platform Support

### 12.1 gocryptfs Platform Matrix

From [gocryptfs's README](https://github.com/rfjakob/gocryptfs#platforms):

| Platform | gocryptfs | FUSE | Encrypted Vaults | Notes |
|---|---|---|---|---|
| **Linux** | ✅ Native | ✅ kernel FUSE | ✅ Full support | Primary platform. All features. |
| **macOS** | ✅ Beta | ✅ macFUSE | ✅ Works | "Most things work fine but occasional problems." Requires macFUSE install. |
| **FreeBSD** | ⚠️ Unknown | ✅ fusefs (kernel) | ⚠️ Untested | Go FUSE library (go-fuse) targets Linux & macOS. No FreeBSD testing reported. Likely won't compile without patches. |
| **Windows (native)** | ❌ No | ❌ No | ❌ No | gocryptfs doesn't support Windows. There's an independent C++ port ([cppcryptfs](https://github.com/bailey27/cppcryptfs)) but it's an unrelated project. |
| **Windows (WSL2)** | ✅ | ✅ kernel FUSE | ✅ Works | WSL2 runs a real Linux kernel with FUSE. gocryptfs works. This is the recommended path for Windows users. |

### 12.2 What Works Without gocryptfs/FUSE?

Vevault itself (`vv`) has no FUSE dependency for non-encrypted vaults:

| Feature | Without gocryptfs/FUSE | With gocryptfs/FUSE |
|---|---|---|
| Create unencrypted vaults | ✅ Yes | ✅ Yes |
| Sync unencrypted vaults | ✅ Yes | ✅ Yes |
| Backup unencrypted vaults | ✅ Yes | ✅ Yes |
| Create encrypted vaults | ❌ No (`--encrypt` requires gocryptfs) | ✅ Yes |
| Mount encrypted vaults | ❌ No (requires gocryptfs + FUSE) | ✅ Yes |
| Sync encrypted vaults | ✅ Yes (syncs ciphertext, just can't mount) | ✅ Yes |
| Backup encrypted vaults | ✅ Yes (backs up ciphertext + keys) | ✅ Yes |
| `vv copy clone|import` encrypted | ❌ No (needs temp gocryptfs mount) | ✅ Yes |
| Subscribe to encrypted vault | ✅ Yes (receives ciphertext + keys) | ✅ Yes (can mount) |
| Restore encrypted vault from backup | ✅ Yes (restores ciphertext + keys) | ✅ Yes (can then mount) |

**Key insight:** A host without gocryptfs can still participate in the vault network.
It can subscribe to encrypted vaults, sync ciphertext, and serve as a backup target.
It just can't mount and work with the files locally. This is fine for NAS devices,
headless backup servers, or thin clients.

### 12.3 Detection & Graceful Degradation

```
$ vv init
  ✓ gocryptfs found (/usr/bin/gocryptfs v2.6.0)
  ✓ rclone found (/usr/bin/rclone v1.70.0)
  ⚠ restic not found — backups disabled until installed

$ vv init
  ✓ rclone found
  ⚠ gocryptfs not found — encrypted vaults disabled
      Install: go install github.com/rfjakob/gocryptfs/v2@latest
  The rest of vevault works fine without it.
```

`vv vault create --encrypt` on a host without gocryptfs prints a clear error and
tells the user what to install.

### 12.4 Future: Non-FUSE Encrypted Access

For platforms without FUSE (FreeBSD, native Windows), a future release could
provide a fallback using rclone crypt:

```
vv copy clone personal ~/export/       # Decrypt via rclone crypt (no FUSE)
vv copy import personal ~/import/      # Encrypt via rclone crypt (no FUSE)
```

This would give basic encrypted vault access without FUSE, using rclone (which
runs on all platforms). It's less convenient than a mount but covers the use case.
Deferred to v2+.

### 12.5 FreeBSD Action Plan

1. Test `gocryptfs` compilation on FreeBSD (go-fuse may or may not support it).
2. If go-fuse doesn't work: FreeBSD's `fusefs(5)` kernel module exists; we could
   contribute FreeBSD support to go-fuse or gocryptfs.
3. Fallback: FreeBSD users use `vv copy clone|import` via rclone crypt (v2+).
4. Fallback: FreeBSD users use unencrypted vaults. Sync, backup, and restore
   all work regardless of encryption.

---

## 13. What We're NOT Doing

| Idea | Why not |
|---|---|
| Custom FUSE filesystem in Go | Massive engineering effort for marginal benefit. Security risk. See §3. |
| rclone crypt instead of gocryptfs | No FUSE. Forces copy-in/copy-out for every operation. Bad UX. |
| Plaintext + encrypted mirror (dual directory) | Not "at rest" encryption. Double disk usage. Confusing. |
| Whole-disk / block-level encryption | LUKS already exists. Vevault encrypts at the vault level, not the disk level. |
| Per-file key encryption | Unnecessary complexity. One key per vault is sufficient for this threat model. |
| Age tool for key encryption | Age is great, but introduces another binary dependency. NaCl secretbox is 20 lines of Go. We use it for key files only. |

---

## 14. Open Questions

### Q1: Should gocryptfs be bundled or detected?

**Recommendation:** Detect at runtime, offer to download. Like rclone.
- Check `PATH` for `gocryptfs`.
- If missing, print: `gocryptfs is required for encrypted vaults. Install: ...`
- Future: `vv setup` downloads rclone + gocryptfs + restic in one go.

Bundling statically-linked binaries for linux/amd64, linux/arm64, darwin/amd64,
darwin/arm64 is a release engineering challenge. Detect-and-link is simpler for v1.1.

### Q2: What about `gocryptfs -reverse`?

gocryptfs reverse mode takes a plaintext directory and exposes an encrypted view.
Could be useful for `vv copy` without temp mounts. But reverse mode is read-only,
so it only helps `vv copy clone`, not `vv copy import`.

**Recommendation:** Defer. Temp mount for copy operations is simpler and handles
both directions.

### Q3: Should `vv mount` run gocryptfs in the foreground or background?

Foreground (blocking) or background (daemon)?

**Recommendation:** Background by default (`gocryptfs` already backgrounds by default).
- `vv mount personal ~/Personal` starts gocryptfs, prints "mounted at ~/Personal",
  and exits. gocryptfs daemon stays running.
- `vv mount --foreground personal ~/Personal` for debugging (keeps gocryptfs in
  foreground, logs to stderr).

### Q4: How do we handle `gocryptfs.conf` during sync/conflicts?

`.gocryptfs.conf` is a static file — it doesn't change after vault creation.
It's the same on every host. No conflict risk.

`gocryptfs.diriv` (per-directory IVs) are generated by gocryptfs during normal
operation and will be synced with the vault data. If two hosts generate different
IVs for the same directory (unlikely), rclone bisync's `.conflict` mechanism
handles it. The user can delete the `.conflict` file — gocryptfs will regenerate
the IV on next access.

### Q5: Can I use my own gocryptfs config?

Yes. If you've already got a gocryptfs filesystem, you can import it:

```
vv vault create personal --encrypt --gocryptfs-conf /path/to/.gocryptfs.conf
```

`vv` will copy the config into `vaults/personal/` and extract the key for
`keys/personal`. This is an advanced use case but avoids lock-in.

---

## 15. Summary of Recommendations

| # | Decision | Rationale |
|---|---|---|
| 1 | **Vault IS ciphertext on disk** | True at-rest encryption. No dual-directory confusion. |
| 2 | **gocryptfs for FUSE** | Audited, battle-tested. Same "shell out to expert" pattern as rclone. |
| 3 | **NaCl secretbox only for key files** | Small scope (32 bytes), low risk. No FUSE dependency needed. |
| 4 | **One shared master password across all hosts** | Simplest model. One user, one password, many devices. Encrypted key files are synced everywhere anyway. |
| 5 | **`vv copy clone|import` for no-mount operations** | Quick mount → copy → unmount behind the scenes. |
| 6 | **Back up ciphertext + keys** | Double encryption (gocryptfs + restic) is defense in depth, not waste. |
| 7 | **gocryptfs detected at runtime** | Like rclone. Download option in future. |
| 8 | **Argon2id for master key derivation** | Modern, memory-hard. Standard since RFC 9106. |

---

## 16. Next Steps

1. **Decide on gocryptfs vs internal** (this document recommends gocryptfs).
2. **Update Design.md** to remove the dual-directory model and reflect the
   gocryptfs integration approach.
3. **Update backup.md** §8.1 (encrypted vault backup — now resolved: back up ciphertext).
4. **Implement Phase 1** — key management (internal/crypto with NaCl secretbox
   for key files, Argon2id for master key derivation).
5. **Implement Phase 2** — gocryptfs mount/umount integration.
6. **Update `vv init`** to detect gocryptfs, offer setup hints.