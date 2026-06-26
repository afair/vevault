# VeVault — Web Interface

> **Status:** Design & evaluation. Target: v1.2 or v2.
> **Relevant:** [Design §2](Design.md), [Distributed Architecture](distributed.md)

---

## 1. Motivation

SSH + rclone covers the happy path: users with terminals and SSH keys. But there are
gaps:

| Gap | Example |
|---|---|
| **No SSH client** | Phone, tablet, locked-down corp machine, guest device |
| **No terminal comfort** | Non-technical family member sharing a vault |
| **Quick file grab** | Download one file from a vault without syncing the whole thing |
| **Bulk download** | Grab an entire vault as a `.tar.gz` for offline use or migration |
| **Light browsing** | Check what's in a vault before deciding to subscribe |
| **API access** | Script or CI pipeline that needs to fetch/push vault contents over HTTP |

A web interface bridges these gaps without requiring SSH keys, `rclone`, or terminal
familiarity on the client side.

---

## 2. Design Principles

1. **SSH remains primary.** The web interface is a convenience surface, not a
   replacement for `vv sync`. No sync happens over HTTP — this is read-only browsing
   + download.

2. **Localhost only, nginx in front.** `vv web` binds to `127.0.0.1:<port>`. TLS,
   authentication, rate-limiting, and WAN exposure are delegated to nginx (or caddy,
   haproxy, etc.). This keeps the Go server simple and auditable.

3. **Single binary, zero config.** The server, templates, and static assets are
   embedded in the `vv` binary (`embed.FS`). `vv web` starts and you're done.

4. **Mobile-first UI.** The primary use case for non-SSH access is phones and
   tablets. The UI must be responsive and touch-friendly.

5. **Stateless.** The server reads files directly from vault directories. No cache,
   no session state, no database beyond the existing BoltDB (read for vault config
   only).

---

## 3. Architecture

```
                          TLS (nginx)           HTTP (localhost)
┌──────────┐         ┌──────────────────┐      ┌──────────────┐
│ Browser  │────────▶│ nginx / caddy    │─────▶│ vv web       │
│ (phone,  │  :443   │ - TLS termination│ :9090│ - file browse│
│  tablet, │         │ - basic auth     │      │ - tar.gz     │
│  desktop)│         │ - rate limiting  │      │ - file view  │
└──────────┘         │ - path rewrites  │      └──────┬───────┘
                     └──────────────────┘             │
                                                      │ reads directly
                                             ┌────────▼──────┐
                                             │ ~/.local/share│
                                             │ /vevault/     │
                                             │   vaults/     │
                                             │   config.toml │
                                             └───────────────┘
```

`vv web` binds to `127.0.0.1:9090` by default (high port, configurable). nginx
proxies requests from the outside world, handling:

- TLS termination (Let's Encrypt)
- HTTP basic auth (`htpasswd`)
- Rate limiting (prevent brute-force against basic auth)
- Path stripping/rewriting (serve at `/vaults/` or a custom subdomain)
- IP allowlists (optional)

This keeps the attack surface of `vv web` itself minimal: it only accepts plain HTTP
from localhost and doesn't implement auth, TLS, or rate limiting.

---

## 4. CLI

```
vv web [--addr <host:port>] [--read-only]
```

| Flag | Default | Description |
|---|---|---|
| `--addr` | `127.0.0.1:9090` | Listen address. Must be `127.0.0.1` or `::1` unless `--expose` is passed. |
| `--expose` | `false` | Allow binding to non-loopback interfaces. Prints a loud warning. |
| `--read-only` | `true` | For v1, only read-only is supported. Flag placeholder for future write support. |
| `--profile` | `vevault` | Which profile's vaults to serve (same as global `--profile`). |

The server runs in the foreground. A systemd unit (or launchd on macOS) can daemonize
it:

```ini
# ~/.config/systemd/user/vv-web.service
[Unit]
Description=VeVault web interface
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/vv web
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

---

## 5. Routes

### 5.1 HTML (browser-facing)

| Method | Path | Description |
|---|---|---|
| `GET` | `/` | Vault index — list all vaults with name, file count, size |
| `GET` | `/v/{vault}/` | Browse vault root directory |
| `GET` | `/v/{vault}/{path...}` | Browse subdirectory or view file |

### 5.2 File actions

| Method | Path | Query | Description |
|---|---|---|---|
| `GET` | `/v/{vault}/{path...}` | `?dl=1` | Force download (Content-Disposition: attachment) |
| `GET` | `/v/{vault}/{path...}` | `?raw=1` | Serve as raw (Content-Type from extension, inline) |
| `GET` | `/v/{vault}/{path...}` | — | Directory listing if path is a dir, file view otherwise |
| `GET` | `/v/{vault}/{path...}` | `?tgz=1` | Download directory (or entire vault) as `.tar.gz` |

`?tgz=1` on a vault root (`/v/personal/`) archives the entire vault. On a
subdirectory (`/v/personal/photos/2024/`) it archives just that subtree.

### 5.3 Streamed tar.gz

The tar.gz is generated on-the-fly with `archive/tar` + `compress/gzip`, streaming
to the client as it walks the directory. No temp files on disk. The server sets:

```
Content-Type: application/gzip
Content-Disposition: attachment; filename="vault-name-2026-06-25.tar.gz"
Transfer-Encoding: chunked
```

For large vaults, this avoids buffering gigabytes in memory. The client sees
download progress naturally from the chunked transfer.

### 5.4 Future: API (v2)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/vaults` | List vaults (JSON) |
| `GET` | `/api/v1/vaults/{name}` | Vault metadata + root listing (JSON) |
| `GET` | `/api/v1/vaults/{name}/{path...}` | Directory listing or file metadata (JSON) |
| `POST` | `/api/v1/vaults/{name}/{path...}` | Upload file (multipart) |
| `DELETE` | `/api/v1/vaults/{name}/{path...}` | Delete file/directory |

API auth would use bearer tokens configured in `config.toml` (not basic auth —
that's nginx's domain for human users).

---

## 6. UI Design

### 6.1 Layout

```
┌──────────────────────────────────────────────────┐
│  VeVault                    [≡ menu]  [profile]  │  ← top bar, sticky
├──────────────────────────────────────────────────┤
│  / personal / photos / 2024 /                    │  ← breadcrumbs
├──────────┬───────────────────────────────────────┤
│          │                                        │
│  📁 2023 │  📄 IMG_001.jpg     2.3 MB  2024-03  │  ← file list (columns:
│  📁 2024 │  📄 IMG_002.jpg     1.8 MB  2024-04  │     icon, name, size,
│  📁 2025 │  📄 notes.txt        12 KB  2024-05  │     modtime)
│          │                                        │
│          │                                        │
│          │  ┌─────────────────────────────────┐  │
│          │  │ File preview (text)              │  │  ← right pane:
│          │  │                                  │  │     selected file
│          │  │ Line 1 of selected file...       │  │     preview
│          │  │ Line 2 of selected file...       │  │
│          │  │ ...                              │  │
│          │  └─────────────────────────────────┘  │
│          │  [Download] [Download .tar.gz]        │  ← action bar
├──────────┴───────────────────────────────────────┤
│  vault: personal  │  142 files  │  2.1 GB        │  ← footer stats
└──────────────────────────────────────────────────┘
```

### 6.2 Views

**Vault index (`/`):**
- Grid of vault cards: name, description, file count, size, last modified
- Click a card → browse that vault

**Directory view:**
- Left: tree sidebar (vaults as top-level nodes, expandable subdirectories)
- Right: file list table (sortable by name, size, modtime)
- Breadcrumb nav at top
- "Download as .tar.gz" button for current directory
- Multi-select checkboxes for batch download (v2)

**File view:**
- Text files: syntax-highlighted preview (auto-detect language from extension)
- Images: inline preview with lightbox
- Binary files: hex dump (first few KB) + "Download" button
- PDFs: embed via browser's native PDF viewer
- All files: download button, metadata (size, modtime, permissions)

### 6.3 Mobile

On narrow viewports (<768px):
- Single-column layout (no sidebar)
- Hamburger menu for vault switching
- File list stacks vertically with larger tap targets
- Swipe left/right on images in lightbox (v2)

---

## 7. Template & Asset Strategy

Everything is embedded via `embed.FS`:

```
internal/web/
├── web.go             # Server setup, route registration, handlers
├── templates/
│   ├── base.html      # Layout skeleton (nav, footer, CSS/JS includes)
│   ├── index.html     # Vault list
│   ├── browse.html    # Directory listing + file preview
│   ├── error.html     # Error page (404, 403, 500)
│   └── login.html     # (v2, if built-in auth is added — skip for v1)
├── static/
│   ├── css/
│   │   └── style.css  # Single CSS file, no framework
│   └── js/
│       └── app.js     # Minimal JS: file preview, mobile nav toggle
└── assets/
    └── icons/         # Inline SVG icons for file types (folder, image, text, etc.)
```

**No npm, no build step.** CSS is hand-written (~300 lines), JS is vanilla (~200 lines).
SVG icons are inlined in templates or served as static files. The Go binary compiles
and embeds everything in one step.

Templates use `html/template` with the following data types:

```go
type IndexData struct {
    Vaults []VaultEntry   // name, path, file count, total size, last mod
}

type BrowseData struct {
    Vault     string         // current vault name
    Path      string         // relative path within vault
    Breadcrumbs []Crumb      // path segments for breadcrumb nav
    Entries   []DirEntry     // files and dirs in current directory
    Parent    string         // parent directory path (empty at root)
    Stats     DirStats       // file count, total size for this dir
}

type DirEntry struct {
    Name    string
    IsDir   bool
    Size    int64
    ModTime time.Time
    Mode    os.FileMode      // for permission display
    Icon    string           // CSS class for icon
}
```

---

## 8. Security

### 8.1 Path Traversal

Request paths like `/v/personal/../../etc/passwd` must not escape the vault
directory. The handler resolves the full path and validates it is a prefix of
the vault root:

```go
func safePath(vaultRoot, relPath string) (string, error) {
    full := filepath.Clean(filepath.Join(vaultRoot, relPath))
    if !strings.HasPrefix(full, vaultRoot) {
        return "", ErrPathTraversal
    }
    return full, nil
}
```

### 8.2 Symlink Traversal

Symlinks within a vault that point outside the vault must either:
- Be resolved and validated against the vault root (follow but check destination), or
- Show the symlink target in the UI but block access with a warning.

**Recommendation: follow symlinks, validate destination is within the vault.** If
a symlink escapes the vault, return 403. Otherwise, serve the resolved file as normal.
This matches how a local user would interact with the vault (shell follows symlinks).

### 8.3 Large File / DoS

- `http.MaxBytesReader` caps request body size (for future upload support).
- tar.gz streaming naturally rate-limits downloads (one file at a time).
- nginx `limit_req` and `limit_conn` handle aggressive request patterns.
- No in-memory buffering of large responses — everything streams.

### 8.4 Directory Listing Privacy

Only vaults defined in `config.toml` are served. The vault index page lists only
vaults, not arbitrary directories. Hidden files (dotfiles) are shown by default
(matching `ls -a` behavior), but can be toggled off with a query param `?hidden=0`
on the UI.

### 8.5 Authentication — Three Tiers

| Tier | Mechanism | Use Case |
|---|---|---|
| **Tier 1: nginx basic auth** | `htpasswd` file, nginx `auth_basic` | Human users. Simple, universal, no vv code needed. |
| **Tier 2: nginx mTLS** | Client certificates | High-security environments. nginx validates certs, no vv changes. |
| **Tier 3: Built-in tokens** | Bearer tokens in `config.toml` (v2 for API) | Programmatic access. `vv web` validates tokens for API routes. |

For v1, Tier 1 is the recommendation and the only documented path. Example nginx config:

```nginx
server {
    listen 443 ssl;
    server_name vaults.example.com;

    ssl_certificate     /etc/letsencrypt/live/vaults.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vaults.example.com/privkey.pem;

    auth_basic "VeVault";
    auth_basic_user_file /etc/nginx/.htpasswd;

    location / {
        proxy_pass http://127.0.0.1:9090;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

---

## 9. Comparison: Alternatives

Before building, it's worth asking: do we need to build anything?

| Alternative | Pros | Cons |
|---|---|---|
| **rclone serve http** | Already installed, configurable, supports auth | Generic file server; no vault awareness, no tar.gz, no tree view |
| **rclone serve webdav** | Standard protocol, Finder/Nautilus mountable | Not browser-friendly for casual browsing |
| **FileBrowser** | Mature, polished UI, auth, uploads, multiple users | Separate binary, separate config, not vault-aware |
| **Caddy file_server** | Zero config, auto-TLS, browse template | No tar.gz download, no vault concept, plain directory listing |
| **Build it into vv** | Vault-aware, tar.gz downloads, single binary, embedded | Development cost |

**Recommendation: Build it into vv.** The alternatives are generic file servers. The
value of `vv web` is vault awareness — listing only configured vaults, vault-level
tar.gz downloads, breadcrumb nav that reflects vault structure, and integration with
the existing `config.toml`. If we're going to tell someone "install FileBrowser, here's
a config," we might as well give them `vv web` which reads the same config.

That said, for a **v1 MVP**, `rclone serve http` on the vault directory, behind nginx,
is a 5-minute solution that covers the "download files from a browser" use case. `vv web`
is the polished long-term answer.

---

## 10. Implementation Plan

### Phase 1 — Read-only browser (v1.2 target)

| # | Task | Effort |
|---|---|---|
| 1 | `internal/web/web.go`: HTTP server setup, route registration, `--addr` flag | Small |
| 2 | Path safety: traversal validation, symlink resolution | Small |
| 3 | Templates: `index.html`, `browse.html`, `error.html` | Medium |
| 4 | Static assets: `style.css`, `app.js`, SVG icons | Medium |
| 5 | Handlers: vault list, directory browse, file view | Medium |
| 6 | `?dl=1` force-download and `?tgz=1` tar.gz streaming | Medium |
| 7 | CLI integration: `vv web` subcommand in cobra | Small |
| 8 | Documentation: nginx config examples, systemd unit, README section | Small |

### Phase 2 — Polish (v2)

| # | Task | Effort |
|---|---|---|
| 9 | Tree sidebar with lazy-loaded subdirectory expansion | Medium |
| 10 | Image lightbox, syntax highlighting for text files | Medium |
| 11 | File sort (click column headers to sort by name/size/date) | Small |
| 12 | Multi-file select + batch download as tar.gz | Medium |
| 13 | Dark mode (follow OS preference via `prefers-color-scheme`) | Small |

### Phase 3 — API & write support (v2+)

| # | Task | Effort |
|---|---|---|
| 14 | REST API routes (`/api/v1/...`) with bearer token auth | Medium |
| 15 | File upload via multipart form and API | Medium |
| 16 | File delete, rename, mkdir via API | Medium |
| 17 | `config.toml` token management (`vv web token create`) | Small |

---

## 11. Open Questions

### Q1: Write support?

Current design is **read-only**. Should users be able to upload files and create
directories through the web UI?

Arguments for read-only:
- Write introduces sync complexity (if central is also syncing with remotes while a
  web user uploads a file, rclone bisync could conflict).
- SSH + `vv sync` and `vv copy import` handle writes already.
- Security: a compromised web session can't destroy vault data.

Arguments for write support:
- Full round-trip: a mobile user can not only grab files but also add them.
- "Dropbox-like" experience for family members.

**Recommendation: read-only for v1. Write support in v2 with clear caveats:**
uploads go to a staging area (or trigger an immediate bisync with central if on a
remote), and the UI shows a warning: "Changes may not appear on other devices until
the next sync."

### Q2: Should `vv web` serve encrypted vaults?

If a vault has `encryption = true`, the files on disk are ciphertext. `vv web` would
need to decrypt on the fly.

Options:
- Skip encrypted vaults in the web UI (show "encrypted — not available for web
  browsing").
- Decrypt in-memory for each request (performance cost).
- Require the vault to be unlocked first (`vv vault unlock <name>` via FUSE or temp
  decryption).

**Recommendation: skip for v1, display a clear message.** Encrypted vault web access
depends on the encryption design (FUSE mount vs on-the-fly). Revisit when encryption
lands in v1.1.

### Q3: Should each vault have its own nginx location?

```
location /personal/ { proxy_pass http://127.0.0.1:9090/v/personal/; }
location /work/     { proxy_pass http://127.0.0.1:9090/v/work/; }
```

This allows different auth rules per vault (e.g., `personal` is open to family,
`work` requires mTLS). nginx handles this — `vv web` doesn't need to know.

**Recommendation: document as an advanced nginx pattern, not a vv feature.**

### Q4: Per-vault read-only flag?

A vault could have `web_readonly = false` to opt out of web access entirely:

```toml
[[vaults]]
name = "secrets"
web_access = false     # Not exposed via vv web
```

**Recommendation: yes, add this to the vault config schema.** Simple, clear, and
prevents accidental exposure of sensitive vaults through the web interface.

### Q5: Multiple profiles?

If a user runs `vv --profile work web`, only work-profile vaults are served. Running
two instances on different ports is possible for serving multiple profiles
simultaneously. No special handling needed — each `vv web` instance reads its own
`config.toml`.

### Q6: Directory listing format — table or grid?

Table with columns (name, size, modified) is more information-dense and sortable.
Grid of cards with thumbnails is prettier for media vaults.

**Recommendation: table as default, grid as a view toggle (`?view=grid`).** For v1,
table only. Grid in v2.

---

## 12. Summary of Recommendations

| # | Decision | Rationale |
|---|---|---|
| 1 | **Read-only** for v1 | Avoid sync conflicts, lower security risk |
| 2 | **localhost only + nginx in front** | Tiny attack surface, leverages existing TLS/auth tooling |
| 3 | **nginx basic auth** for human users | Universal, zero code in vv |
| 4 | **Embedded templates + assets** via `embed.FS` | Single binary, no build step |
| 5 | **Streaming tar.gz** for downloads | No disk buffering, handles large vaults |
| 6 | **Path traversal protection** mandatory | Non-negotiable security boundary |
| 7 | **Symlinks: follow + validate** | Matches local shell behavior |
| 8 | **Skip encrypted vaults** in v1 | Depends on encryption design (v1.1) |
| 9 | **`web_access = false` per vault** | Opt-out for sensitive vaults |
| 10 | **Table view (v1), grid toggle (v2)** | Information density > prettiness for MVP |
| 11 | **v1.2 target** | After encryption (v1.1) and backup polish, before multisite |