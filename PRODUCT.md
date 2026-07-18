# gaderno

## Register
product

## Users & purpose
People running local Jupyter-style notebooks who want a **server-owned, collaborative** document: edit and execute in the browser, kernels on the host, thin client. Desk tool — not a marketing site, not JupyterHub.

Primary tasks: open a workspace, open/create notebooks, edit cells, run code, see outputs, co-edit/chat with another tab.

## Personality
Quiet · Spacious · Polished · Quiet delight (cobalt signal; Go **G** mark as logo only)

## Visual direction
- **daisyUI 5 + Tailwind 4**: source in `styles/input.css`, `bun run build:css` → embedded `internal/web/static/app.css`
- **Spacier product UI** that stays usable on mobile (edit + run is the bar)
- **Icon chrome**: G · session status · chat · avatar — icons only on mobile
- **Cells**: play + execution count in the gutter; markdown preview-first; Colab-style insert gaps
- Themes: `gaderno-light` / `gaderno-dark`
- Restrained palette: pure base + cobalt primary; **gopher cyan (`#00ADD8`) for the logo chip only**

## Anti-references
- Classic JupyterLab chrome clone (heavy sidebars, purple Lab skin, permanent In/Out + Run on every row)
- Finance/money green–gold product skins
- Cream/sand SaaS backgrounds
- Glassmorphism, gradient text, hero KPI cards
- Dense “instrument packing” that breaks on ~390px

## Design principles
1. Cells and outputs first — chrome stays thin and icon-first.
2. Space for reading and tapping; not a marketing landing page.
3. Explicit controls over hotkeys (v1).
4. Trust: clear run / sync / kernel state; no fake sandbox cues.
5. Mobile edit+run works; kernel/export/theme progressive via session + avatar.
