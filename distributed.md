# Distributed Architecture for Vevault

> **Status:** Evaluation — comparing hub-and-spoke (current) vs distributed vs federated models.
> **Recommendation:** Stay hub-and-spoke for v1.x; add relay capabilities in v2 as a pragmatic middle ground.

---

## 1. Terminology

| Term          | Definition |
|---------------|-----------|
| **Hub-and-spoke** | One central host; all remotes sync through it (current). |
| **Distributed** | Every host is a peer; any host can sync with any other (like git). |
| **Federated** | Multiple centrals form a mesh; each serves a local group of remotes. Like email (SMTP) or Mastodon/ActivityPub. |
| **Relay** | A special mode: a remote that also acts as central for downstream remotes (hierarchical hub-and-spoke). |

---

## 2. Use Cases That Drive Distributed/Federated

### 2.1. NAT / Firewall Traversal

```
┌─────────┐     SSH      ┌─────────┐
│  Laptop  │────────────▶│Central  │  ← public or Tailscale
└─────────┘              └─────────┘
                              ▲
                              │ SSH (initiated by central)
                         ┌────┴─────┐
                         │Air (NAS) │  ← behind NAT, no inbound SSH
                         └──────────┘
```

**Problem:** Central can't reach Air because Air is behind NAT and has no inbound SSH.

**Hub-and-spoke workaround:** Air initiates an outbound SSH tunnel to central, then central syncs through the tunnel. Works but adds complexity.

**Distributed fix:** Air could sync directly with Laptop when both are on the same LAN, without central.

### 2.2. Multi-Site / Multi-Cluster

```
Site A (office):                    Site B (home):
┌─────────┐                         ┌─────────┐
│Central A│◄──────────WAN──────────►│Central B│
└────┬────┘                         └────┬────┘
     │                                   │
 ┌───┴───┐                           ┌───┴───┐
 │Host A1│                           │Host B1│
 └───────┘                           └───────┘
```

**Problem:** Each site has local hosts that sync fast over LAN. Cross-site syncs over WAN are slower. You want local centrals for LAN speed, but global convergence.

### 2.3. Offline / Eventually Consistent

Devices that go offline regularly (laptops, phones). When they come online, they sync with whoever is reachable, not necessarily a single designated central.

### 2.4. No Always-On Server

Users who don't have a server. They want laptop ↔ desktop sync without investing in an always-on host.

---

## 3. Architecture Comparison

### 3.1. Hub-and-Spoke (Current)

```
              ┌─────────┐
        ┌────▶│ Central │◀────┐
        │     └─────────┘     │
   ┌────┴───┐            ┌────┴───┐
   │ Laptop │            │  NAS   │
   └────────┘            └────────┘
```

**Sync flow:**
1. Laptop changes file → `vv sync` → SSHs to central: `vv updates laptop`
2. Central runs `rclone bisync central ↔ laptop`
3. Central propagates to NAS: `rclone bisync central ↔ nas`

| Pros | Cons |
|------|------|
| Simple mental model | Central is a SPOF (single point of failure) |
| One source of truth (central config) | Must have an always-on central |
| rclone bisync is 2-way, fits naturally | All syncs go through central (bottleneck) |
| Easy conflict resolution (central always wins ties) | Remote hosts behind NAT need workarounds |
| Configuration is centralized | |
| Easy to audit — one log on central | |

### 3.2. Distributed (git-like)

```
    ┌─────────┐         ┌─────────┐
    │ Laptop  │◄───────►│   NAS   │
    └────┬────┘         └────┬────┘
         │                   │
         │    ┌─────────┐    │
         └───►│ Desktop │◄───┘
              └─────────┘
```

Every host is a peer. Any host can sync with any other. No designated central.

**Sync flow:**
1. Laptop changes file → `vv sync nas` → bisync laptop ↔ nas
2. Later, Desktop syncs: `vv sync nas` → bisync desktop ↔ nas
3. Or `vv sync --all` → syncs with every known peer

| Pros | Cons |
|------|------|
| No SPOF | Conflict resolution is **much** harder (n-way merge) |
| Works without always-on server | rclone bisync is 2-way — doesn't support n-way |
| Direct peer sync when on same LAN | Version vectors or CRDTs required for convergence |
| Survives any host going offline | Each host must know about all others (topology explosion) |
| git-like mental model (familiar) | Which peer has the latest config? |
| | Configuration drift between peers |
| | Hard to audit — logs scattered across hosts |

**Key challenge: conflict resolution.**

With hub-and-spoke, at most two hosts change a file between syncs (central and one remote). rclone bisync handles this.

With distributed, three hosts could change the same file:
- Laptop edits `todo.txt` offline (old state: "A")
- Desktop edits `todo.txt` offline (old state: "A" → "B")
- Phone edits `todo.txt` offline (old state: "A" → "C")
- All three come online. What's the result?

Git solves this with explicit merge + conflict markers. File sync tools can't do that without user intervention. rclone bisync would flag `.conflict1` / `.conflict2` files but someone has to resolve them manually.

### 3.3. Federated (Mastodon/ActivityPub-like or email/SMTP-like)

```
Site A (office):                    Site B (home):
┌─────────┐                         ┌─────────┐
│Central A│◄════════WAN═══════════►│Central B│
└────┬────┘   (rclone or rsync)    └────┬────┘
     │                                   │
 ┌───┴───┐ ┌───┴───┐               ┌───┴───┐
 │Host A1│ │Host A2│               │Host B1│
 └───────┘ └───────┘               └───────┘
```

Multiple centrals, each authoritative for a group of local hosts. Centrals sync with each other.

**Sync flow:**
1. Host A1 changes file → central A: `vv sync` (delegates to Central A)
2. Central A syncs with Host A1 (bisync)
3. Central A syncs with Central B (bisync or rsync)
4. Host B1 syncs with Central B — gets the change

| Pros | Cons |
|------|------|
| LAN-speed local syncs | More complex than hub-and-spoke |
| Survives WAN partition (local syncs still work) | Cross-site syncs have higher latency |
| Each central is authoritative for its group | Centrals must trust each other |
| Reduces central bottleneck | Two layers of bisync (centrals ↔ each other) |
| Still uses 2-way bisync (centrals talk pairwise) | More configuration to maintain |
| | Conflict resolution between centrals |

### 3.4. Relay / Hierarchical Hub-and-Spoke

```
              ┌─────────┐
              │ Central │  ← always-on cloud server
              └────┬────┘
                   │
         ┌─────────┼─────────┐
         ▼         ▼         ▼
    ┌─────────┐ ┌─────┐ ┌─────────┐
    │ Laptop  │ │ NAS │ │ Air(MBP)│ ← "relay" for downstream hosts
    └─────────┘ └─────┘ └────┬────┘
                              │
                         ┌────┴─────┐
                         │ Pi (NAS) │  ← behind Air's firewall
                         └──────────┘
```

A pragmatic middle ground: some remotes can act as relays for downstream remotes.

**Sync flow:**
1. Pi changes file → SSHs to Air: `vv sync`
2. Air syncs with Pi (bisync) — Air is Pi's "central"
3. Air SSHs to top-level Central: `vv updates air`
4. Top-level Central syncs with Air (bisync)

| Pros | Cons |
|------|------|
| Still uses 2-way bisync everywhere | Chain of trust: Pi trusts Air, Air trusts Central |
| Handles hosts behind NAT/firewall | Longer sync chain = higher latency |
| Minimal new concepts (just "relay" flag) | If Air is offline, Pi can't sync to wider network |
| Each relay has its own config | State could diverge if relay goes down mid-sync |
| | Cascading failures |

---

## 4. Conflict Resolution Deep Dive

| Model | Conflicts | Resolution |
|-------|-----------|------------|
| Hub-and-spoke | 2 parties (central + 1 remote) | rclone bisync: `.conflict1`/`.conflict2` files. Central wins ties. |
| Federated | 3+ parties across centrals | Each central-level bisync produces conflict files. Manual resolution needed at each hop. |
| Distributed | N parties, any topology | rclone can't handle this. Need CRDTs (Automerge, Yjs) or version vectors + manual merge. Massive complexity. |
| Relay | 2 parties at each hop | Same as hub-and-spoke, but cascading. A conflict at hop 2 may not be visible at hop 1. |

**Bottom line:** rclone bisync is fundamentally a 2-party tool. Distributed/federated sync multiplies the conflict surface but doesn't provide a resolution mechanism for it.

---

## 5. Configuration Challenge

### Hub-and-spoke (current)
```
One config file (on central), synced to all hosts.
Central defines: vaults, subscriptions, paths.
Remotes are thin clients.
```

### Distributed
```
Each host has its own config.
Who defines: which vaults exist? who is subscribed to what?
If Laptop creates a vault, how do NAS and Desktop learn about it?
Configuration becomes a sync problem (chicken-and-egg).
```

### Federated
```
Each central has its own config for its local group.
A federation config defines: which centrals talk to each other, which vaults cross sites.
Two-tier configuration.
```

Vevault's current design relies on a single authoritative config. Moving to distributed would require a distributed config mechanism — essentially a separate consensus problem.

---

## 6. Recommendation

### For v1.x: Stay hub-and-spoke

The current model is:
- **Simple** — users understand it immediately
- **Correct** — bisync's 2-party model maps 1:1
- **Auditable** — one log on central shows all activity
- **Sufficient** — covers 80%+ of use cases (one server, multiple devices)

### For v2: Add relay mode

A relay is a remote host that also acts as central for downstream remotes. This is the smallest conceptual addition with the biggest practical gain:

```
# On Air (a macOS laptop that can reach the NAS behind its firewall):
[core]
central_host = "cloud-server"
relay = true       # Air accepts sync requests from downstream hosts

# On Pi (NAS behind Air's firewall):
[core]
central_host = "air"  # Pi treats Air as its central
```

**Why relay over distributed/federated:**
1. Still uses 2-way bisync at every hop — no new conflict resolution needed
2. Minimal config changes — just a `relay` field and per-hop path overrides
3. Handles the most common distributed need: hosts behind NAT
4. Each hop is an independent hub-and-spoke relationship

**Why not full distributed:**
1. rclone bisync can't do n-way — we'd need a completely different sync engine
2. Conflict resolution becomes a research problem (CRDTs, version vectors)
3. Configuration distribution needs its own consensus protocol
4. Massive increase in code complexity for marginal gain
5. Git already exists for distributed version control — vevault is file sync

**Why not federated:**
1. Adds complexity for a use case (multi-site) that most individual users don't have
2. Requires managing trust between centrals
3. Two-tier sync introduces latency and potential divergence

---

## 7. Open Questions

1. **Should we support direct LAN sync?** If Laptop and NAS are on the same LAN, should `vv sync` prefer direct bisync over routing through central? (Yes, in v2 — auto-detect LAN peers via mDNS.)

2. **Should a relay propagate or just serve?** When Air acts as relay for Pi, should changes from Pi propagate back up to the top-level central automatically, or only when Pi explicitly syncs?

3. **Relay chain depth?** How many levels of relay nesting should we support? (Recommend: 2 levels max — top-level central → relay → leaf.)

4. **What about Tailscale?** Tailscale already solves NAT traversal. If all hosts are on Tailscale, the need for relays drops significantly. Should we focus on Tailscale integration instead?

5. **Should we support rclone serve for on-demand sync?** Instead of full bisync, expose vaults via rclone serve SFTP/WebDAV. Mount remote vaults on demand. Pros: no sync conflicts. Cons: requires connectivity.

---

## 8. Decision Log

| Decision | Date | Rationale |
|----------|------|-----------|
| Hub-and-spoke for v1 | 2026-06 | Simplicity, correctness with bisync, covers 80%+ use cases |
| Relay mode deferred to v2 | 2026-06 | Adds most value for least complexity; NAT/firewall cases |
| Distributed deferred indefinitely | 2026-06 | Requires different sync engine; git already covers this space |
| Federated deferred indefinitely | 2026-06 | Multi-site is niche; can be approximated with relay + Tailscale |
