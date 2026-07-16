# gaderno

## Register
product

## Users & purpose
People running local Jupyter-style notebooks who want a **server-owned, collaborative** document: edit and execute in the browser, kernels on the host, thin client. Desk tool — not a marketing site, not JupyterHub.

Primary tasks: open a workspace, open/create notebooks, edit cells, run code, see outputs, co-edit/chat with another tab.

## Personality
Quiet · Dense · Instrument-like (cobalt signal on steel-neutral paper)

## Visual direction
- **daisyUI 5 + Tailwind 4** (same pipeline pattern as contapila: `bun run build:css` → embedded `app.css`)
- **Jupyter familiarity** for cell semantics (In/Out, code vs markdown, run affordances)
- **Contapila-class density** for chrome: tight sticky topbar, `btn-xs` / `table-xs`, no airy marketing cards
- Themes: `gaderno-light` / `gaderno-dark`
- Restrained palette: pure base + cobalt primary (hue ~250°)

## Anti-references
- Classic JupyterLab chrome clone (heavy sidebars, purple Lab skin)
- Contapila green/gold money theme (different product)
- Cream/sand SaaS backgrounds
- Glassmorphism, gradient text, hero KPI cards

## Design principles
1. Cells and outputs first — chrome stays thin.
2. Density like a lab notebook, not a landing page.
3. Buttons over hotkeys (v1).
4. Trust: clear run/error/sync state; no fake sandbox cues.
