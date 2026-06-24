# VeVault Backup Design

> **Status:** v1.1 — design & recommendations. No backup code exists yet.
> **Relevant:** [Requirements §Backups](Requirements.md), [Design §10](Design.md), [Automation](automation.md)

---

## 1. Current State

Backup is deferred to **v1.1**. `internal/backup/backup.go` is a stub. There is no
config schema for backup entries, no cli surface, and no integration with `restic`.

Current `config.toml` has **no backup fields** on `VaultConfig`:

```go
type VaultConfig struct {
    Name       string   `toml:"name"`
    Path       string   `toml:"path,omitempty"`
    Symlinks   []string `toml:"symlinks,omitempty"`
    Encryption bool     `toml:"encryption"`
    // No Backup field yet
}
```

---

## 2. What Was Originally Planned

### 2.1 Separate Restic Repos Per Vault (Design.md §10)

```toml
[[vaults]]
name = "personal"
[backup]
enabled   = true
repo      = "sftp:backup-server:/backups/personal"
password  = "${RESTIC_PASSWORD}"
schedule  = "daily"
retention = { daily = 7, weekly = 4, monthly = 6 }
```

### 2.2 3-2-1 Strategy (Design.md §10.3)

Multiple backup destinations per vault:
```toml
[[vaults]]
name = "personal"
[[backups]]
repo   = "sftp:nas:/backups/personal"
schedule = "daily"
[[backups]]
repo   = "s3:s3.amazonaws.com/bucket"
schedule = "weekly"
```

### 2.3 Backup Flow (Design.md §10.2)

```
vv backup all                          # All enabled vaults + config + keys
vv backup vault <name>                 # Single vault
vv backup restore <name> <target>      # Restore one vault
```

### 2.4 Backup After Sync (your idea)

The intuition is sound: sync converges all hosts to a consistent state on central,
so that moment is the right time to take a snapshot. If backup follows every sync,
you always have a recent, coherent copy.

---

## 3. Key Design Questions

### Q1: Who runs the backup?

| Option | Pros | Cons |
|---|---|---|
| **Option A: Central only** | Single place to manage repos & creds. After sync, central has everything. Simple. | Backups only as good as last sync. Remote changes sitting offline are unprotected. |
| **Option B: Every host** | Each host protects local changes immediately, even pre-sync. Survives central failure. | Credential distribution problem. Duplicated data across repos. No single restore surface. |
| **Option C: Central after sync + on-demand on remotes** | Central is primary backup surface. Remote can `vv backup vault personal` pre-sync for safety. | Slightly more complex. Remote repos must be configured. |

**Recommendation: Option C.**
- Central runs `vv backup all` as a **post-sync hook** (automatic or manual).
- Non-central hosts can run `vv backup vault <name>` explicitly before disconnecting
  or before a risky operation (e.g., `vv backup vault personal` on a laptop before
  travel). This is an escape hatch, not the default.
- Central is the primary backup surface. Remotes back up locally as insurance.

### Q2: When does backup run?

Three trigger points:

| Trigger | When | Default? |
|---|---|---|
| **Post-sync (automatic)** | Central runs backup immediately after `vv updates` completes for a host. | ✅ Yes, opt-out per vault (`post_sync_backup = false`). |
| **Post-sync (manual / flag)** | `vv sync --backup` or `vv backup all` after `vv sync`. | Good fallback. |
| **Scheduled (cron/systemd)** | Timer invokes `vv backup all`. | For vaults without frequent sync, or as a safety net. |

**Recommendation: post-sync automatic backup, with scheduled as a safety net.**

Post-sync makes the most sense because:
1. Central just completed `rclone bisync` — it has the freshest possible data.
2. No extra timer config needed for the happy path.
3. A conflict or sync failure **skips** the backup (don't back up stale/inconsistent data).

Scheduled backups (daily via systemd timer) serve as a fallback for vaults that
don't sync frequently, or for the vevault config/keys which change rarely.

### Q3: Per-vault repos vs unified backup?

The original design says "separate restic repo for each vault." This has advantages:
- **Isolation:** Restore `personal` without touching `work`.
- **Retention per vault:** `personal` keeps 90 days; `work` keeps 30 days.
- **Independent repos:** Different backends per vault (NAS for personal, S3 for media).

But it also means more repos to manage (init, password, prune).

**Recommendation: per-vault repos by default, with an optional unified fallback.**

```toml
# Per-vault (primary pattern)
[[vaults]]
name = "personal"
[backup]
enabled = true
repo    = "sftp:nas:/backups/personal"

# Unified catch-all (for simplicity / small vaults)
[core.backup]
repo = "sftp:nas:/backups/vevault-all"
# Vaults without individual backup config use this.
```

This preserves the original vision (per-vault) while offering a low-friction
entry path for users who just want "backup everything."

### Q4: What exactly gets backed up?

| Item | When | Notes |
|---|---|---|
| `vaults/<name>/` | Per-vault backup | The plaintext working copy. If encrypted, back up **plaintext** (central has the key). |
| `encrypted/<name>/` | Never (by default) | Backing up the encrypted tree is redundant — we can always re-encrypt from plaintext. Only back up encrypted if you don't have the key on the backup host. |
| `keys/<name>.age` | With vevault config backup | **Critical.** Losing keys = losing data. Must be backed up. |
| `config.toml` | With vevault config backup | So you can reconstruct vault definitions after total loss. |
| `state.db` | With vevault config backup | Sync timestamps; nice-to-have, not critical. |

**Recommendation: back up vault plaintext + config + keys.**

On central, `vv backup all` does:
1. For each vault with `backup.enabled`: `restic backup <vault_path>`
2. A separate snapshot of `~/.local/share/vevault/keys/` + `config.toml` (to a
   dedicated `vevault-config` repo, or tagged in the vault repo of the first
   configured vault).

### Q5: What happens on sync failure?

**Recommendation: skip the post-sync backup.**

If `rclone bisync` exits non-zero, `vv` should:
1. Log the failure.
2. **Not** run restic backup.
3. Optionally run a scheduled fallback backup later (if timer is configured).

Rationale: a failed sync means data may be out of date or inconsistent. We don't
want to snapshot a partial or stale state.

### Q6: What about in-progress syncs & concurrent access?

Restic reads files in the vault. If a file is being written during backup, restic
may capture a partial file. Two approaches:

1. **Locking:** Acquire a `vv.lock` in the vault before sync *and* before backup.
   Serializes all access. Heavy-handed.
2. **Best-effort:** Accept that restic's dedup will clean up any partially-captured
   files on *next* backup. rclone already writes atomically (write-to-temp, rename),
   so partially-written files are rare in practice.

**Recommendation: best-effort (approach 2).**
- rclone bisync writes files atomically (or close to it).
- restic is a file-level snapshot; worst case you get a pre-write version, which is fine.
- A lock adds complexity for marginal benefit in a single-central model.

If we later allow multiple concurrent syncs on central (currently serialized in code),
we should reconsider locking.

---

## 4. Architecture Recommendations

### 4.1 Backup Flow on Central (Post-Sync)

```
vv updates laptop personal
  │
  ├─ 1. rclone bisync central ↔ laptop (personal)     ✓ success
  ├─ 2. Propagate to other subscribers                 ✓ success
  └─ 3. restic backup <vault_path> --tag host=laptop   (if post_sync_backup)
       restic forget --prune <retention>                (if configured)
```

Step 3 is skipped if:
- `backup.enabled = false` (per vault)
- `backup.post_sync_backup = false` (per vault)
- Sync failed
- restic not installed / repo not reachable (log warning, don't fail sync)

### 4.2 Backup on Non-Central Hosts (On-Demand)

```
laptop$ vv backup vault personal
  │
  ├─ 1. Find vault config for "personal"
  ├─ 2. restic -r <repo> backup <local_vault_path>
  └─ 3. restic -r <repo> forget --prune <retention>  (optional)
```

This is explicit and manual. Hosts don't auto-backup after delegated sync — that's
central's job. If a remote user wants pre-disconnect safety, they run it themselves.

### 4.3 Scheduled Backup (systemd Timer)

```ini
# ~/.config/systemd/user/vv-backup.service
[Unit]
Description=Vevault scheduled backup

[Service]
Type=oneshot
ExecStart=/usr/local/bin/vv backup all
StandardOutput=append:%h/.local/share/vevault/backup.log
StandardError=append:%h/.local/share/vevault/backup.log
```

```ini
# ~/.config/systemd/user/vv-backup.timer
[Unit]
Description=Vevault backup timer

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

Scheduled backup catches vaults that haven't synced recently, and ensures the
config/keys snapshot stays current.

### 4.4 Backup During vv sync --config

When config is pushed to all hosts, should we also backup config? No — config is
rarely changed. The scheduled backup covers it.

---

## 5. Configuration Design

### 5.1 Per-Vault Backup Config

```toml
[[vaults]]
name = "personal"

# Single backup destination (common case)
[backup]
enabled       = true
repo          = "sftp:nas.local:/backups/personal"
password_cmd  = "pass show restic/personal"      # Or password_file, or RESTIC_PASSWORD_FILE
post_sync     = true                              # Auto-backup after successful sync
retention     = { daily = 7, weekly = 4, monthly = 6 }
exclude       = ["*.tmp", "node_modules/", ".git/"]
```

### 5.2 Multiple Backup Destinations (3-2-1)

```toml
[[vaults]]
name = "personal"

[[backups]]
repo   = "sftp:nas:/backups/personal"
schedule = "post-sync"          # Backup after every sync
retention = { daily = 7, weekly = 4 }

[[backups]]
repo   = "s3:s3.amazonaws.com/bucket/vaults/personal"
schedule = "weekly"             # Offsite, weekly via cron
retention = { weekly = 4, monthly = 6 }
```

When multiple `[[backups]]` are defined:
- `post-sync` backups run immediately after sync to matching repos.
- `daily`/`weekly`/`monthly` repos are backed up by the scheduled timer.

### 5.3 Unified Backup Fallback

```toml
[core.backup]
repo = "sftp:nas:/backups/vevault-all"
# Vaults without their own backup config use this.
# The vevault config + keys snapshot also goes here.
```

### 5.4 Credential Management

Restic needs a repository password. Options, in order of preference:

| Method | Config field | Notes |
|---|---|---|
| `RESTIC_PASSWORD` | (none — env var) | Standard restic env. Works transparently. |
| `RESTIC_PASSWORD_FILE` | (none — env var) | Points to a file. Good for systemd. |
| `password_cmd` | `password_cmd = "pass show restic/personal"` | Avoids writing password to disk. Requires the command to be available. |
| `password_file` | `password_file = "/secrets/restic-personal"` | Plain file. Least secure. |

**Recommendation:** Support `password_cmd` as the primary config-driven approach,
and respect `RESTIC_PASSWORD` / `RESTIC_PASSWORD_FILE` env vars (restic's native
mechanism). Do NOT store passwords in config.toml.

---

## 6. CLI Design

```
vv backup all
    Back up all vaults with backup enabled. Also snapshots config + keys.
    Behavior varies by host:
      - Central: backs up all vaults, then config/keys.
      - Non-central: backs up local vaults with backup config.

vv backup vault <name>
    Back up a single vault to all its configured repos.

vv backup restore <name> <target> [--snapshot <id>]
    Restore vault <name> from its backup repo to <target>.
    --snapshot restores a specific snapshot; default is latest.

vv backup list <name>
    List snapshots for a vault.

vv backup check [<name>]
    Run restic check on the vault's repo(s). Verifies integrity.

vv backup init <name>
    Initialize restic repo for a vault. Run once per repo.

vv backup config
    Back up just the vevault config and keys.

vv backup config restore <target>
    Restore config + keys from the backup repo to <target>.
```

Flags:
```
--no-forget     Skip retention policy (don't prune old snapshots).
--dry-run       Show what would be backed up without doing it.
--tag <tag>     Add a custom tag to the restic snapshot.
```

---

## 7. Implementation Plan

### Phase 1: Foundation — restic wrapper & config

1. **Expand `VaultConfig`** to include `BackupConfig`:
   ```go
   type BackupConfig struct {
       Enabled      bool              `toml:"enabled"`
       Repo         string            `toml:"repo"`
       PasswordCmd  string            `toml:"password_cmd,omitempty"`
       PasswordFile string            `toml:"password_file,omitempty"`
       PostSync     bool              `toml:"post_sync"`
       Retention    RetentionPolicy   `toml:"retention,omitempty"`
       Exclude      []string          `toml:"exclude,omitempty"`
   }

   type RetentionPolicy struct {
       Daily   int `toml:"daily,omitempty"`
       Weekly  int `toml:"weekly,omitempty"`
       Monthly int `toml:"monthly,omitempty"`
       Yearly  int `toml:"yearly,omitempty"`
   }
   ```

2. **Build `internal/backup/` package**:
   - `Repo` type wrapping a restic repository (URL, password resolution).
   - `NewRepo(cfg BackupConfig) (*Repo, error)` — validates, resolves password.
   - `Repo.Backup(paths []string, tags []string) error` — runs `restic backup`.
   - `Repo.Forget(policy RetentionPolicy) error` — runs `restic forget --prune`.
   - `Repo.Snapshots() ([]Snapshot, error)` — list snapshots.
   - `Repo.Restore(snapshotID, target string) error`.
   - `Repo.Check() error`.
   - `Repo.Init() error` — initialize a new restic repository.

3. **CLI registration** in `cmd/vv/main.go`. Add `backup` as a top-level command
   with subcommands: `all`, `vault`, `restore`, `list`, `check`, `init`, `config`.

### Phase 2: Post-sync integration

4. **Hook into `runUpdates()` in `internal/sync/sync.go`**:
   - After successful bisync + propagation, check `backup.post_sync`.
   - If enabled, call `backup.BackupVault(vaultName)`.
   - Log failure but don't fail the sync.

5. **`vv backup all` on central** iterates all vaults + config.

### Phase 3: Scheduling & automation

6. **systemd timer unit** generation (`vv backup schedule` or documented in README).
7. **`vv backup all` supports `--schedule` flag** to differentiate ad-hoc from
   scheduled runs (different tags, different retention maybe).

### Phase 4: Multi-destination (3-2-1)

8. Support `[[backups]]` array on vault config (multiple repos, different schedules).
9. Tag-based routing: `post-sync` repos get backed up after sync; `daily`/`weekly`
   repos get backed up by timer.

---

## 8. Things to Resolve / Open Questions

### 8.1 Backup of encrypted vaults

When encryption (v1.1) is enabled, central has both:
- `vaults/<name>/` (plaintext, unlocked with key)
- `encrypted/<name>/` (ciphertext)

**Question:** Should we back up plaintext or ciphertext?

**Recommendation: plaintext, on central only.**
- Central has the key material (`keys/<name>.age`).
- Backing up ciphertext without the key is useless.
- Backing up plaintext means the backup repo's restic encryption is the only
  encryption layer — and that's fine (restic encrypts by default).
- The `keys/` directory is backed up separately (config backup).

If a user wants encrypted-at-rest even in backups, they can point restic at
`encrypted/<name>/` instead. We can support `backup.source = "plaintext" | "encrypted"`.

### 8.2 Should `vv sync --backup` be a thing?

I lean **no**. Post-sync backup should be configured per-vault (`backup.post_sync`)
and enabled by default. A flag per invocation is a band-aid for missing config.
If someone really wants one-off backup, `vv backup vault <name>` already exists.

### 8.3 Handling large vaults

A bisync of a 100GB vault takes time. A restic backup of the same vault also takes
time. Running them serially doubles the wall-clock time.

**Mitigations:**
- restic is incremental — only changed files are re-read.
- `--exclude` patterns can skip large, reproducible directories (`.git/`, `node_modules/`).
- `backup.post_sync = false` for vaults where sync frequency >> backup need.
- Future: `vv sync --no-backup` to skip post-sync backup on a specific run.

### 8.4 What about `restic check`?

restic repos should be verified periodically. `vv backup check <name>` runs
`restic check --read-data` (expensive) or `restic check` (metadata only).

**Recommendation:** Add `vv backup check` as a manual command. Add a weekly
`vv backup check --quick` to the systemd timer. Don't run full `--read-data`
without user opt-in (it's slow and I/O-intensive).

### 8.5 Restore testing

Users should periodically verify that restores work. Options:
- `vv backup test-restore <name>` — restore to a temp dir, check file count matches.
- Documented manual process.

**Recommendation:** Manual for v1.1. A `--test` flag on `vv backup restore` that
restores to a temp dir and reports differences.

### 8.6 Notification of backup failures

If post-sync backup fails, how does the user know?
- Current sync output is terminal (stdout/stderr).
- `vv sync` exits zero on sync success even if backup fails (by design — backup
  is best-effort).
- Print a **warning** to stderr: `Warning: backup of vault 'personal' failed: ...`
- Log to `~/.local/share/vevault/backup.log` for audit.

No email/webhook alerts in v1.1. Defer to v2.

### 8.7 `restic` dependency

Like `rclone` for sync, `restic` is required for backup. It should be:
- Detected at backup time with a clear error if missing.
- Checked in `vv init` with a warning.
- Not required for non-backup operations.

---

## 9. What We're NOT Doing (Anti-Goals)

| Idea | Why not |
|---|---|
| Versioned archive of deleted files per vault | restic already provides snapshot history. Redundant. |
| `vv backup` runs rclone to push to remote backup target | restic already handles transport (SFTP, S3, etc.). Don't duplicate. |
| Backup-as-sync — using restic to distribute vaults between hosts | Sync is `rclone bisync`'s job. restic is for disaster recovery, not distribution. |
| Real-time continuous backup (inotify + restic) | Too frequent; restic snapshots work better at sync boundaries. |
| Encrypted backup repos managed by vevault (keygen, etc.) | restic handles its own encryption. Just pass the password. |
| Backup of remote hosts' vaults from central (push backup) | Central doesn't SSH into remotes for SFTP; only rclone does. Backup is local. |

---

## 10. Summary of Recommendations

| # | Decision | Rationale |
|---|---|---|
| 1 | **Per-vault repos** (with optional unified fallback) | Isolation, retention per vault, different backends. |
| 2 | **Post-sync backup on central** (automatic) | Sync converges data; that's the ideal backup moment. |
| 3 | **On-demand backup on non-central hosts** | Escape hatch for laptops pre-disconnect. Not automatic. |
| 4 | **Skip backup on sync failure** | Don't snapshot stale/inconsistent state. |
| 5 | **Back up plaintext, not ciphertext** (when encrypted) | Plaintext + keys gives full recovery. Redundant ciphertext backup is waste. |
| 6 | **Separate config + keys snapshot** | Critical metadata backed up independently from vault data. |
| 7 | **password_cmd + env vars for creds** | Never store passwords in config.toml. |
| 8 | **Backup failure = warning, not error** | Backup is best-effort. Sync should not fail because restic is unreachable. |
| 9 | **systemd timer for scheduled fallback** | Safety net for vaults that sync infrequently. |
| 10 | **restic is a dependency** (detected at runtime) | Like rclone for sync. Clear error if missing. |

---

## 11. Next Steps

1. **Decide on the open questions** in §8, especially §8.1 (encrypted vault backup).
2. **Finalize `BackupConfig` struct** and TOML schema.
3. **Implement `internal/backup/`**: restic wrapper, password resolution, repo init.
4. **Wire up CLI**: `vv backup *` subcommands.
5. **Integrate post-sync hook** into `runUpdates()`.
6. **Document**: systemd timer setup, 3-2-1 strategy, restore procedures.
