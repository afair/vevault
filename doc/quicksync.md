# QuickSync — 1-Way Push Sync Evaluation

> **Status:** Evaluation — assessing value and implementation of a lightweight 1-way push
> from central to multiple subscriber hosts as a complement to full rclone bisync.

---

## 1. The Problem

### What exists today

Every sync is a full `rclone bisync` — a 2-way, bidirectional synchronization between
central and each subscribing host:

```
central$ vv updates laptop personal
  → rclone bisync central ↔ laptop     # Full 2-way comparison, both directions
  → rclone bisync central ↔ workstation # Propagate to each subscriber
  → rclone bisync central ↔ phone
```

`rclone bisync` does heavy work per invocation:
- Lists both sides' full directory trees
- Compares modtime + size for every file on both sides
- Determines what needs to go in each direction
- Copies new/changed files both ways
- Propagates deletions both ways
- Tracks state files to detect conflicts

For a vault with 10,000 files and 10 subscribers, central runs 10 full-tree comparisons
per sync, even if nothing changed on 9 of the 10 hosts.

### Where 1-way push is a better fit

| Use case | 2-way bisync | 1-way push |
|---|---|---|
| **Dotfiles** — edited on one machine, deployed everywhere | ❌ Overkill. Don't want changes from random hosts flowing back into the dotfiles repo. | ✅ Central pushes out; remotes just consume. |
| **Reference vaults** — curated media library, docs, papers | ❌ No one else should write to these. | ✅ Central is the authority, period. |
| **Deploy configs to many VMs/containers** | ❌ Each VM triggering bisync back is wasteful + wrong. | ✅ Push-and-forget. |
| **Collaborative work vault** — multiple people editing files | ✅ Correct model. Both sides have changes. | ❌ Would lose changes from remote hosts. |
| **Personal documents** — laptop + desktop both edit | ✅ Both sides are writers. | ❌ Would clobber changes from non-central. |

### The concrete gap

There's no way in vevault today to say: *"This vault is read-only on subscribers. Central
pushes; they receive. No fan-out, no 2-way comparison, no conflict files."*

The user must either:
1. Use full bisync and accept the overhead + wrong semantics (changes on remotes flow back)
2. Not use vevault and `rsync` / `scp` manually

---

## 2. Proposed Solution: QuickSync Mode

### Concept

A per-subscription mode flag that controls how central syncs with each subscriber.
The same vault can have bisync subscribers (collaborators who read and write) and
push subscribers (read-only consumers who only receive).

```toml
# On central's config.toml

# Collaborative hosts — full 2-way bisync
[[subscriptions]]
host   = "laptop"
vaults = ["dotfiles"]
mode   = "bisync"      # default, can be omitted

# Read-only virtual hosts — 1-way push from central
[[subscriptions]]
host   = "vhost01"
vaults = ["dotfiles"]
mode   = "push"

[[subscriptions]]
host   = "vhost02"
vaults = ["dotfiles"]
mode   = "push"
```

Mixed modes for the same host are handled by splitting into separate subscription
entries (one per mode):

```toml
# workstation: dotfiles are push-only, but personal is collaborative
[[subscriptions]]
host   = "workstation"
vaults = ["dotfiles"]
mode   = "push"

[[subscriptions]]
host   = "workstation"
vaults = ["personal", "work"]
# mode omitted → defaults to bisync
```

This is cleaner than a vault-level flag because it doesn't force all-or-nothing on
the vault. A dotfiles vault can serve both: laptop edits and syncs bidirectionally;
vhost01-vhost50 just receive.

### Commands

```
# Subscribe hosts in push mode (triggers initial push from central)
vv subscribe dotfiles --push vhost01,vhost02,vhost03
# Regular bisync subscription (default):
vv subscribe dotfiles --host laptop

# Push a vault to all push-mode subscribers
vv sync dotfiles --all

# Push to specific hosts (only meaningful for push-mode subscribers)
vv sync dotfiles --push vhost01,vhost03

# New top-level convenience command (alternative UX)
vv push dotfiles --all
vv push dotfiles --host vhost01,vhost02
```

### How it works under the hood

Instead of `rclone bisync`, central runs `rclone sync`:

```
# Current (bisync):
rclone bisync /central/vaults/dotfiles/ :sftp,host=vhost01:/remote/path \
    --metadata --create-empty-src-dirs --force

# QuickSync (1-way):
rclone sync /central/vaults/dotfiles/ :sftp,host=vhost01:/remote/path \
    --metadata --create-empty-src-dirs --delete-excluded
```

**Key differences:**

| Aspect | `rclone bisync` | `rclone sync` (QuickSync) |
|---|---|---|
| Direction | Both ways | Central → remote only |
| File comparison | Full tree walk both sides | Source tree walk; dest checks modtime/size only for changed files |
| Deletions | Propagated both ways | Files deleted on central are deleted on remote; files created on remote are **left alone** (not pulled back) |
| State files | Yes (`.bisync.*` state tracking) | No persistent state |
| Conflict files | Yes (`.conflict1`/`.conflict2`) | N/A — no conflicts possible |
| Remote changes | Flow back to central | **Ignored** — remote is a consumer |
| Performance | O(files_central + files_remote) | O(files_central) — roughly 2x faster on read-heavy vaults |
| Network | Transfers in both directions | Transfers only central → remote |

### Concurrency

With 50 virtual hosts all subscribed to a push-only dotfiles vault, central can push to
them in parallel (fan-out):

```
vv push dotfiles --all
  → Spawn N concurrent rclone sync workers (configurable: --parallel 10)
  → Each worker syncs to one host independently
  → No ordering dependencies between hosts
```

This is impossible with bisync because bisync with host A modifies central's state, which
would confuse concurrent bisyncs with host B. With 1-way push, central's source directory
is read-only — no state mutation — so full parallelism is safe.

---

## 3. Value Assessment

### Quantitative wins

| Scenario | 10 hosts, 5,000 files | 50 hosts, 10,000 files |
|---|---|---|
| **bisync (current)** | 10 × O(5,000 + 5,000) full comparisons. ~10s per host = 100s serial | 50 × O(10,000 + 10,000). ~20s per host = 1,000s serial |
| **sync (QuickSync)** | 10 × O(5,000) source-side only. ~5s per host = 50s serial, or ~5s total with concurrency | 50 × O(10,000). ~10s per host = 500s serial, or ~50s total with `--parallel 10` |
| **Gain** | 2× serial, up to 10× with concurrency | 2× serial, up to 20× with concurrency |

For the dotfiles use case specifically, the win is larger because dotfiles vaults typically
have many small files and change infrequently. Bisync still walks the full tree every time;
`rclone sync` only checks remote modtimes for files that exist, with no remote→central scan.

### Qualitative wins

1. **Correct semantics for read-only subscribers.** VMs that receive dotfiles shouldn't
   be able to push changes back. Today bisync would happily pull their local modifications
   into central and propagate them everywhere — a subtle correctness bug waiting to happen.

2. **No conflict files to clean up.** A `.conflict1` on a dotfiles vault that propagates
   to 50 hosts is a nightmare. With push-only, conflicts can't happen.

3. **No bisync state corruption.** Power loss or network interruption during bisync leaves
   stale state files that require `--resync` to recover. `rclone sync` has no persistent
   state to get corrupted.

4. **Suitable for cron/systemd automation.** A daily `vv push dotfiles --all` is trivial
   to automate. Bisync automation requires monitoring for stale state, conflict files,
   failed comparisons.

5. **Enables the "many virtual hosts" use case.** A central configuration server pushing
   out to dozens of lightweight VMs/containers. This is currently impractical with bisync
   due to serial execution and per-host overhead.

6. **Layering surface.** QuickSync could later support:
   - Post-push hooks (e.g., `stow` after receiving dotfiles)
   - Per-host subdirectory filtering (`rclone --include`/`--exclude`)
   - File templating (`.vv.tmpl` files rendered per-host before push)

### What it doesn't replace

QuickSync is explicitly **not** a replacement for bisync. It's a complementary mode for
vaults where one-way semantics are correct. Collaborative vaults (multiple writers) still
use bisync. The user chooses at vault creation time.

---

## 4. Implementation Plan

### Phase 1 — Core engine (smallest viable slice)

**Estimated effort:** ~1 day for a working prototype.

1. **Add `Mode` to subscription config:**
   ```go
   // internal/config/config.go
   type Subscription struct {
       Host    string            `toml:"host"`
       Address string            `toml:"address,omitempty"`
       Vaults  []string          `toml:"vaults"`
       Mode    string            `toml:"mode,omitempty"`  // NEW: "bisync" (default) or "push"
       Paths   map[string]string `toml:"paths,omitempty"`
   }

   // Helper so callers don't check strings everywhere.
   func (s *Subscription) IsPush() bool { return s.Mode == "push" }
   ```

   A subscription without an explicit mode defaults to bisync (backward-compatible).
   Mixed modes for the same host are expressed as separate subscription entries —
   each with its own mode.

2. **Add `pushSyncVault` function in `internal/sync/sync.go`:**
   ```go
   func pushSyncVault(cfg *config.Config, vaultName, host string) error {
       localPath := cfg.VaultPath(vaultName)
       remotePath := cfg.RemoteVaultPath(vaultName, host)
       args := []string{
           "sync", localPath,
           fmt.Sprintf(":sftp,host=%s:%s", cfg.HostAddress(host), remotePath),
           "--metadata", "--create-empty-src-dirs",
           // Note: no --delete-excluded by default; let users opt in
       }
       // ... same filter/exclude logic as bisyncVault ...
   }
   ```

3. **Add `subscriptionMode` lookup and branch in `runUpdates`:**
   ```go
   func subscriptionMode(cfg *config.Config, host, vaultName string) string {
       for _, s := range cfg.Subscriptions {
           if s.Host == host {
               for _, v := range s.Vaults {
                   if v == vaultName {
                       if s.Mode == "push" {
                           return "push"
                       }
                       return "bisync"
                   }
               }
           }
       }
       return "bisync" // default
   }

   func runUpdates(...) {
       for _, v := range vaults {
           mode := subscriptionMode(cfg, host, v)
           if mode == "push" {
               if err := pushSyncVault(cfg, v, host); err != nil { ... }
               continue // No propagation for push-mode subs
           }
           // Existing bisync path + propagation
           if err := bisyncVault(cfg, v, host, resync); err != nil { ... }
       }
   }
   ```

4. **Add `vv push` subcommand:**
   ```go
   // Registered in cmd/vv/main.go alongside sync
   root.AddCommand(sync.NewPushCmd(cfg))
   ```
   `vv push <vault> [--host <h1> --host <h2> | --all] [--parallel <n>]`

### Phase 2 — Parallel fan-out

**Estimated effort:** ~half a day.

```go
func pushAll(cfg *config.Config, vaultName string, parallel int) error {
    hosts := cfg.SubscribedHosts(vaultName) // New helper
    sem := make(chan struct{}, parallel)
    var wg sync.WaitGroup
    errCh := make(chan error, len(hosts))

    for _, host := range hosts {
        wg.Add(1)
        go func(h string) {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()
            if err := pushSyncVault(cfg, vaultName, h); err != nil {
                errCh <- fmt.Errorf("%s: %w", h, err)
            } else {
                fmt.Printf("  ✓ %s pushed\n", h)
            }
        }(host)
    }
    wg.Wait()
    close(errCh)
    // Collect and report errors
}
```

### Phase 3 — Subscribe UX

**Estimated effort:** ~a few hours.

- `vv subscribe dotfiles --push host1,host2` creates subscription entries with
  `mode = "push"` and triggers initial `rclone sync` (not `rclone bisync --resync`).
- `vv subscribe dotfiles --host laptop` (no `--push`) creates normal bisync
  subscriptions — backward-compatible default.
- On remote: `vv subscribe dotfiles` delegates to central as normal; the remote can
  optionally pass `--push` if it knows it should be a read-only consumer.

### Phase 4 — Post-push hooks (future)

```toml
[[subscriptions]]
host   = "vhost01"
vaults = ["dotfiles"]
mode   = "push"
on_push  = "stow --restow --dir={vault_path} --target=$HOME"
```

After central pushes dotfiles to a host, it SSHs to run the hook. This is the glue that
makes dotfiles vaults actually useful — push then apply.

---

## 5. Design Decisions

### Q: Mode on the vault or the subscription?

**Decision: On the subscription.** The same vault can have both collaborative hosts
(full bisync) and read-only hosts (push). For example, a dotfiles vault where laptop
edits bidirectionally but 50 VMs only receive.

A vault-level flag would force all-or-nothing — either everyone bisyncs or everyone
gets pushed to. That's too coarse.

With per-subscription mode, mixed subscribers are natural:

```toml
# laptop edits dotfiles; bisync flows changes both ways
[[subscriptions]]
host   = "laptop"
vaults = ["dotfiles"]        # mode defaults to bisync

# VMs are read-only consumers
[[subscriptions]]
host   = "vhost01"
vaults = ["dotfiles"]
mode   = "push"
```

When a host needs different modes for different vaults, split into separate
subscription entries — each entry has a single mode that applies to all its vaults.
This keeps the config model simple without a per-vault-per-subscription mode map.

### Q: New `vv push` command or `--push` flag on `vv sync`?

**Decision: Both, but start with `vv sync <vault> --push`.** The `sync` subcommand
already has vault scoping and host targeting. Adding `--push` keeps the surface small.
A separate `vv push` can be added later as a UX sugar alias.

### Q: Should `vv push` also run on non-central hosts?

**Decision: No — delegate to central like everything else.** On a non-central host:

```
laptop$ vv push dotfiles --all
  → ssh central vv push dotfiles --all
```

The non-central host doesn't have SFTP access to all subscribers, and central is
the orchestrator.

### Q: What about `--delete` (remove files on remote that don't exist on central)?

**Decision: Make it configurable, default off.** `rclone sync` without `--delete-excluded`
will leave extra files on the remote alone. This is safer for initial adoption. Users
who want strict mirroring can set `push_delete = true` on the vault config or pass
`--delete` on the CLI.

### Q: Conflict with existing `--push` terminology in remote_plan.md?

**Decision: No conflict.** The `--push` in remote_plan.md was about central pushing
bisync results to specific subscriber hosts. QuickSync is a different mechanism with
different semantics — 1-way sync, no comparison, no conflict files. The old `--push`
idea was already deferred to v1.1; QuickSync replaces and supersedes it.

---

## 6. Comparison with Alternatives

| Approach | Pros | Cons |
|---|---|---|
| **QuickSync (this proposal)** | Simple model, reuses rclone, correct semantics, parallel-safe | Narrower scope than full bisync |
| **`--no-propagate` on subscribe** | Already exists | Doesn't change sync direction; still 2-way |
| **Run `rsync` manually** | Works today | Outside vevault; no config tracking; no subscription management |
| **Separate dotfiles manager (stow, chezmoi, yadm)** | Purpose-built for dotfiles | Another tool to learn, another config format, no vault/backup integration |
| **Make remote vaults read-only at FS level** | Enforces at OS level | Doesn't solve sync efficiency; bisync still compares both ways |

QuickSync is the right level of abstraction: it lives inside vevault, reuses the existing
subscription/vault/host model, and maps cleanly onto `rclone sync` with minimal new code.

---

## 7. Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Users accidentally subscribe a collaborative host with `--push` and lose remote changes | `vv subscribe` prints the mode clearly. `vv vault info` shows mode per subscription. `--push` is an explicit flag, not a default. |
| `rclone sync` deletes files on remote that shouldn't be deleted | Default to no `--delete`. Require explicit `--delete` or `push_delete = true`. |
| Concurrent pushes to the same host (two `vv push` processes) | `rclone sync` handles this naturally — last writer wins, no state corruption. Still warn if a push is already running. |
| Performance with very large vaults and many hosts | Parallel fan-out with configurable `--parallel`. Per-host timeouts via `--timeout`. |

---

## 8. Recommendation

**Adopt QuickSync in v1.1.** It fills a genuine gap in the current architecture:

1. **Dotfiles/config distribution** is the most-requested vevault use case and
   the one least well-served by full bisync. QuickSync makes it correct and efficient.

2. **The implementation is small.** ~100 lines of new Go code in `sync.go`, a new
   config field, and a CLI flag. The heavy lifting is already done by `rclone sync`,
   which is installed everywhere `rclone` is.

3. **It enables the "many virtual hosts" pitch.** "Push your dotfiles to 50 containers
   in parallel" is a compelling feature that differentiates vevault from Syncthing
   (which requires full peer mesh) and rsync (which has no subscription model).

4. **It doesn't complicate the existing model.** Push mode is an opt-in per
   subscription. The default remains bisync. No breaking changes. Mixed subscribers
   (some bisync, some push) on the same vault are supported naturally.

5. **It's a natural stepping stone.** Post-push hooks, templating, and per-host
   filtering all build on the push-only primitive.

### Suggested roadmap order

| Priority | Item |
|---|---|
| 1 | Core engine — subscription `Mode` field + `pushSyncVault` + `vv sync --push` |
| 2 | Parallel fan-out with `--parallel` |
| 3 | `vv push` convenience command |
| 4 | Subscribe UX (`vv subscribe --push`) |
| 5 | Post-push hooks |
| 6 | Per-host include/exclude filters |

---

## 9. Resolved Open Questions

1. **Mode: vault-level or subscription-level?** → **Subscription-level.** A vault can
   have both bisync and push subscribers. Splitting mixed-mode hosts into separate
   subscription entries keeps the model simple: one mode per entry, applies to all
   vaults listed in it. Default is bisync (backward-compatible).

2. **Should remote hosts show their push/bisync mode?** → **Yes.** `vv vault info`
   and `vv vault list` will display "Mode: push (central is authoritative)" or
   "Mode: bisync" so users know what to expect.

3. **Should we add `vv pull` for remotes to explicitly request a push?** → **Defer.**
   Access to central is likely available; `vv sync` already delegates. Revisit if
   it becomes a pain point.

4. **Checksum-based comparison?** → **No.** Simple modtime+size via `rclone sync` is
   sufficient and much faster. No `--checksum` unless user opts in later.