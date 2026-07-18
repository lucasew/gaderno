# gaderno

Local **collaborative notebooks** as a single Go binary: real Jupyter kernels on the host, server-owned document (Yjs/ygo CRDT), thin browser UI.

Name from Portuguese *caderno* (notebook) + Go. Not JupyterLab, not JupyterHub — a desk tool you point at a folder of `.ipynb` files.

Architecture and product contract: [`SPEC.md`](SPEC.md). UI brief: [`PRODUCT.md`](PRODUCT.md) · [`DESIGN.md`](DESIGN.md).

## Features

- **Server is king** — live notebook state lives on the server; clients co-edit over one WebSocket
- **Real kernels** — kernelspecs on the machine, plus optional **uv** synthetic Python kernels (`uv python list`)
- **Lazy kernel start** — bind a kernel on open; process starts on first **play**
- **CodeMirror 6** — Python + markdown, Yjs text sync, remote carets
- **Kernel intelligence** — tab completion, hover docs, signature help (`complete` / `inspect`)
- **Rich outputs** — mime bundles to the client: images (`png`/`jpeg`/`gif`/`svg`) and plain text; **HTML only if the notebook is trusted** (avatar menu)
- **Spacious UI** — play-in-gutter cells, preview-first markdown, Colab-style insert gaps, light/dark themes
- **Session chat** — RAM-only, not saved in the ipynb
- **Disk** — only `.ipynb` on disk; optional shared token gate

## Requirements

| Need | Notes |
|------|--------|
| **Go 1.26+** | via [mise](https://mise.jdx.dev/) (`mise.toml`) or your own toolchain |
| **Bun** | only for building embedded CSS/JS (`mise run codegen`) |
| **A kernel** | classic Jupyter kernelspec **and/or** [`uv`](https://github.com/astral-sh/uv) on `PATH` for synthetic Pythons |

Kernels are **not** bundled in the release binary.

## Install

### Release binary

Download the archive for your OS/arch from [GitHub Releases](https://github.com/lucasew/gaderno/releases), extract `gaderno`, put it on `PATH`.

```bash
gaderno version
```

### From source

```bash
mise run install    # go mod tidy + bun install
mise run ci         # codegen + tests + build check
go build -o gaderno ./cmd/gaderno/
```

## Quick start

```bash
mkdir -p notebooks
gaderno serve --root ./notebooks --listen 127.0.0.1:8765
```

Open **http://127.0.0.1:8765/** → create or open a notebook → pick a kernel (session status control) → **play** a cell.

Optional shared token (anyone who can reach the process + token has full R/W/X).
When set, HTTP and WebSocket require the token (`Authorization: Bearer`, cookie after `?token=…` bootstrap, or a one-shot `POST /api/ws-ticket`).

```bash
export GADERNO_TOKEN=secret
gaderno serve --root ./notebooks --listen 127.0.0.1:8765 --token "$GADERNO_TOKEN"
# open http://127.0.0.1:8765/?token=secret  (cookie set; token stripped from URL)
```

Non-loopback bind without a token is refused unless you pass `--i-understand` (open RCE as the server OS user).

### CLI

| Command | Purpose |
|---------|---------|
| `gaderno serve` | HTTP + WebSocket UI over a workspace root |
| `gaderno version` | Print version (release builds set via GoReleaser ldflags) |

### Flags / env (`serve`)

Flags override env. Prefix `GADERNO_`.

| Flag | Env | Default | Meaning |
|------|-----|---------|---------|
| `--root` | `GADERNO_ROOT` | `.` | Workspace directory of `.ipynb` files |
| `--listen` | `GADERNO_LISTEN` | `127.0.0.1:8080` | Listen address |
| `--token` | `GADERNO_TOKEN` | _(empty)_ | Optional shared access token (enforced when set) |
| `--i-understand` | `GADERNO_I_UNDERSTAND` | `false` | Allow non-loopback listen without a token |
| `--kernel` | `GADERNO_KERNEL` | `python3` | Default kernelspec name hint (no auto-start) |

## Using the UI

- **G** — home / workspace
- **Session status** — connection + sync; click to pick/bind a **kernel**
- **Chat** — session-only panel
- **Avatar menu** — display name, theme, **Trust HTML outputs**, force save, export
- **Play** — run cell (gutter); execution count appears when known
- **Markdown** — preview by default; click to edit, blur to preview
- **Insert** — subtle `+` gaps between cells (Colab-style)

Trust is per notebook (browser `localStorage`). Untrusted notebooks still show images and plain text; `text/html` stays gated.

### Matplotlib / images

```python
import matplotlib.pyplot as plt
import numpy as np

plt.imshow(np.random.rand(32, 32))
plt.show()
```

Outputs ship as full mime bundles; the client renders `image/*` and `text/plain`. Large mimes are capped server-side with a plain-text notice.

## Development

```bash
mise run install
mise run codegen     # embed CSS + CodeMirror bundle
mise run format
mise run ci
```

Layout (high level):

```text
cmd/gaderno/          # CLI entry
internal/
  app/                # HTTP, WS control plane
  kernel/             # discovery, spawn, ZMQ, execute/complete/inspect
  session/            # hub, clients, chat buffer
  crdt/ document/     # ygo notebook + nbformat
  web/                # embedded templates + static
styles/ web/          # Tailwind/daisyUI + editor sources
```

### Release

Tags have **no `v` prefix** (see [`.svu.yml`](.svu.yml)).

Local (needs `GITHUB_TOKEN` with release rights if publishing):

```bash
mise run release          # next (svu) + goreleaser
mise run release patch    # major | minor | patch | next
```

CI ([`.github/workflows/autorelease.yml`](.github/workflows/autorelease.yml)):

- **push / PR / Saturday schedule** — install, codegen, format, `mise run ci`
- **workflow_dispatch** with patch/minor/major — `mise release <bump>` (tag + GoReleaser)
- Updater-bot PR gate (same pattern as contapila / orvalho)

Publishes platform archives + checksums only ([`.goreleaser.yaml`](.goreleaser.yaml)).

## Security notes

- The kernel runs as **the same OS user** as `gaderno` (full host power for that user).
- There is **no multi-tenant isolation**. Do not expose to the open internet without understanding that.
- Prefer `--listen 127.0.0.1:…` or a private network; use `--token` if the port is shared.
- HTML outputs can run browser code only when you explicitly **trust** the notebook.

## License

See repository license file if present; otherwise all rights reserved by the author until stated otherwise.
