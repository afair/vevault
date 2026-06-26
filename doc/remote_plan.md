# Remote Subscription & Sync — Evaluation & Plan

Evaluation of ideas from `remote.md` against the current design. Recommendations
and implementation plan follow.

---

## 1. Multiple Profiles (multiple vevault instances)

> What would being able to subscribe to different centrals look like? Like "git origin" but for vaults.

### Evaluation

This is a clean generalization that adds real value. A user might have:

- `~/.local/share/vevault/` — personal vaults, synced to home server
- `~/.local/share/work-vaults/` — work vaults, synced to office server
- `~/.local/share/friends-vaults/` — shared media vaults, synced to a friend's NAS

Each profile is an independent vevault instance with its own config, central node,
vaults, subscriptions, and keys. No crossover between profiles.

### Recommendation: **Adopt. Low effort, high value.**

The `VV_HOME` env var already provides this mechanism. We add a `--profile` /
`-p` global flag and a `VEVAULT_PROFILE` env var that resolve to
`~/.local/share/<name>/`:

```
vv --profile work vault list
VEVAULT_PROFILE=work vv sync
```

Default profile name is `vevault` (not empty), so the default data dir stays
`~/.local/share/vevault/`.

### Implementation

- Global persistent flag `--profile` on root cobra command.
- `config.Dir()` checks `VEVAULT_PROFILE` env var, then `--profile` flag value.
- Profile directory is `~/.local/share/<profile>/`.
- `vv init --profile work` bootstraps a new profile.

**Question A:** Should `--profile` create the profile directory if it doesn't exist (like `vv init`), or should it require an explicit `vv init` first? Recommendation: require explicit init. Less magic, clearer error messages ("profile 'work' not initialized. Run `vv --profile work init`").

---

## 2. Subscribe from remote hosts

> How do these commands differ? Can we distinguish using the host option?

### Evaluation

Currently, subscriptions must be registered on the central node. The remote.md
proposes that a non-central host should be able to subscribe itself:

```
# On laptop (non-central):
vv subscribe personal --path ~/Documents/Personal
```

This would:
1. Create the local vault directory
2. SSH to central and register `laptop` as a subscriber to `personal`
3. Optionally trigger an initial pull sync

### Recommendation: **Adopt. Better UX.**

A non-central host subscribing itself will:
1. Create the local vault directory
2. SSH to central to register the subscription
3. Trigger an initial pull sync to populate the vault immediately

---

## 3. Sync granularity (--pull, --push, targeted hosts)

> vv sync vaultname --pull hostname / --push hostname

### Evaluation

The current `vv sync` / `vv updates` model is simple but coarse — it always syncs
2-way with the requesting host and propagates to all subscribers. The remote.md
suggests more control:

| Command | What it does |
|---|---|
| `vv sync` | Full sync: pull from requesting host, push to all subscribers (current behavior) |
| `vv sync <vault> --pull <host>` | Pull from a specific host, sync everywhere |
| `vv sync <vault> --push <h1> --push <h2>` | Push central's copy to specific hosts only |
| `vv sync --catch-up` | Pull from central to this host (pre-disconnect sync) |

### Recommendation: **Adopt `--pull` and target-aware sync. Defer `--push`.**

- `--pull <host>` is genuinely useful — central admin pulls from a specific host
  without that host needing to trigger it. E.g., "laptop is online, grab its updates."
- `--push` to specific hosts is niche and adds complexity. The default "propagate to
  all subscribers" covers the common case. Defer to v1.1.
- `--catch-up` (or `--pull` from the non-central side) is useful for pre-disconnect
  syncs. On a non-central host, `vv sync --pull` should tell central "sync with me
  now, but don't propagate — I just want the latest."

### Revised sync subcommands

```
# From central:
vv sync [<vault>]                   Full sync requesting host + propagate all
vv sync [<vault>] --pull <host>     Pull from specific host + propagate all
vv sync [<vault>] --push <host>     Push to specific hosts only (defer)

# From non-central:
vv sync [<vault>]                   Tell central "I have updates" + propagate
vv sync [<vault>] --pull            Pull latest from central, no propagation
```

On non-central, `vv sync --pull` translates to `ssh central vv updates <host> --no-propagate`.

**Question C:** Should `vv publish` / `vv push` exist as aliases on non-central
hosts for "tell central I have updates"? The remote.md suggests clearer naming.
Recommendation: **No.** Two reasons: (a) `vv sync` already does this, (b) on central
`push` means "push to other hosts" but on non-central it means "push to central" —
the asymmetry is confusing. One command, context-aware.

---

## 4. Full sync = two passes

> After a full sync, changes will be on central but not propagated. A second pass
> is required.

### Evaluation

This is already the current design — `vv updates` bisyncs with the requesting host
first, then bisyncs with each subscriber. Both passes happen in one invocation.
No extra step needed. No change required.

---

## 5. Pre-disconnect sync

> Useful for laptops before disconnecting.

### Evaluation

Covered by `vv sync --pull` on non-central hosts (§3 above). Before going offline:

```
laptop$ vv sync --pull
  → ssh central vv updates laptop --no-propagate
  → central bisyncs with laptop (2-way)
  → laptop is fully caught up, central has laptop's changes
  → no propagation to other subscribers (laptop is just catching up)
```

---

## Implementation Plan

### Phase 1 — Foundation (before subscribe/sync expansion)

1. **Profile support**
   - Add `--profile` global flag to root cobra command
   - `config.Dir()` respects `VEVAULT_PROFILE` env var
   - Default profile name: `"vevault"`
   - `vv --profile <name> init` bootstraps a new profile
   - All existing commands work unchanged within their profile

### Phase 2 — Subscribe

2. **`vv subscribe` / `vv unsubscribe` subcommands**
   - New `internal/subscribe/` package
   - On central: add/remove host from subscription list, save config
   - On non-central: SSH to central with subscribe request, create local
     vault directory, optionally `--pull` initial data
   - `unsubscribe` with `--purge` to also remove local vault data

### Phase 3 — Sync improvements

3. **Add `--pull` flag to `vv updates` (central side)**
   - `vv updates <host> --pull` — pull from specific host, propagate
   - `vv updates <host> --pull --no-propagate` — pull only, no propagation

4. **Add `--pull` flag to `vv sync` (non-central side)**
   - `vv sync --pull` → `ssh central vv updates <host> --no-propagate`
   - "Give me the latest, don't bother other hosts"

5. **Deferred: `--push` to specific hosts on central**
   - v1.1 or later

---

## Resolved Questions

| # | Question | Decision |
|---|---|---|
| A | Auto-create profile or require explicit init? | Require explicit `vv --profile <name> init`. |
| B | Pull data immediately on remote subscribe? | Yes — subscribing populates the vault. No separate flag needed. |
| C | Add `vv publish` / `vv push` aliases? | No. |
| D | Global persistent flag on root? | Yes, `--profile` on all subcommands. |
| E | Data dir naming? | `~/.local/share/<name>/`. Default name "vevault". |

---

## Summary

| Idea | Verdict |
|---|---|
| Multiple profiles (`--profile`) | ✅ Adopt — Phase 1 |
| Subscribe from remote hosts | ✅ Adopt — Phase 2 |
| `vv sync --pull <host>` (central) | ✅ Adopt — Phase 3 |
| `vv sync --pull` (non-central, catch-up) | ✅ Adopt — Phase 3 |
| `vv sync --push <host>` (central, targeted) | ⏸️ Defer to v1.1 |
| `vv publish` / `vv push` aliases | ❌ No — `vv sync` covers it |
| Two-pass sync concern | ✅ Already handled in current design |
| Pre-disconnect sync | ✅ Covered by `vv sync --pull` on non-central |

All adopted ideas are compatible with the current architecture, build on existing
patterns (delegation, IsCentral detection), and don't require breaking changes.
