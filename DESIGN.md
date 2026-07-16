# gaderno web UI

## Intent
Notebook product surface with **daisyUI 5 + Tailwind 4**, Jupyter-legible cells, and dense tool chrome (tight sticky topbar, compact controls, full-bleed content).

## Stack
- Source: `styles/input.css` (`@plugin "daisyui"`, custom themes)
- Build: `bun run build:css` → `internal/web/static/app.css` (embedded)
- Themes: `gaderno-light` (default) / `gaderno-dark` (`prefersdark` + toggle)
- Components: navbar, btn, badge, table, input, swap (theme), link

## Color
Cobalt primary (~250° OKLCH). Pure white base in light. Dense radii (`0.25rem`), `--depth: 0`.

## Density
- Navbar `h-10` / `g-navbar` override daisy min-height
- `btn-xs`, `input-xs`, `table-xs`, `badge-xs`
- Cell grid with In/Out prompts; hairline borders not large cards

## Editor
- **CodeMirror 6** (`web/editor/main.js` → `bun run build:js` → `static/editor.js`)
- Python + markdown languages; min height ~8.5rem for code
- Debounced `cell.set_source` over WS; Run flushes source then `exec.run`

## Motion
Minimal; theme toggle only.
