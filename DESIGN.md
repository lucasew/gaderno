# gaderno web UI

## Intent
Notebook product surface with **daisyUI 5 + Tailwind 4**: spacier, polished, mobile-usable. Icon app chrome, play-in-gutter cells, preview-first markdown, closable chat panel.

## Stack
- Source: `styles/input.css` (`@plugin "daisyui"`, custom themes)
- Build: `bun run build:css` → `internal/web/static/app.css` (embedded)
- Themes: `gaderno-light` (default) / `gaderno-dark` (`prefersdark` + avatar menu)
- Components: navbar, btn, badge, menu, modal, input, dropdown

## Color
Cobalt primary (~250° OKLCH). Pure white base in light. **Logo chip only:** Go gopher blue `#00ADD8` behind white **G**. Radii ~0.375–0.5rem for a slightly softer product feel; avoid 24px+ cards.

## Shell
- Sticky topbar: **G** | path | flex | session status | chat | avatar
- Mobile: icon-only actions; path truncates
- Session status: sync/trust; opens kernel dialog
- Chat: full-width closable panel (mobile); same panel model desktop
- Avatar menu: name, theme, export, force-save, kernel, about

## Cells
- Grid: gutter (play + count) | body
- No permanent Run text / Code|Markdown tabs per row
- Cell menu for type / move / delete
- Insert: subtle inter-cell gap with `+` (always tappable; hover emphasis on pointer devices)
- CodeMirror: min-height tracks content more tightly than the old 8.5rem wells
- Markdown: preview default; click to edit; exit restores preview

## Motion
Quiet delight only: panel open/close, play running state, gap hover. Respect `prefers-reduced-motion`.
