import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { python } from "@codemirror/lang-python";
import { markdown } from "@codemirror/lang-markdown";

function readInitialSource(cellId) {
  const el = document.querySelector(
    'script.cell-source-json[data-cell-id="' + CSS.escape(cellId) + '"]'
  );
  if (!el) return "";
  try {
    return JSON.parse(el.textContent || '""');
  } catch (_) {
    return el.textContent || "";
  }
}

/**
 * Mount CodeMirror 6 on every [data-gaderno-editor] host.
 */
export function mountEditors(root, opts) {
  const onChange = (opts && opts.onChange) || function () {};
  const editors = new Map();
  const scope = root || document;

  scope.querySelectorAll("[data-gaderno-editor]").forEach(function (host) {
    const cellId = host.getAttribute("data-cell-id");
    const lang = host.getAttribute("data-lang") || "python";
    const initial = readInitialSource(cellId);
    host.replaceChildren();

    const langExt = lang === "markdown" ? markdown() : python();
    const minH = lang === "markdown" ? 96 : 160;

    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: initial,
        extensions: [
          basicSetup,
          langExt,
          EditorView.lineWrapping,
          EditorView.theme({
            "&": {
              fontSize: "0.8125rem",
              minHeight: minH + "px",
            },
            ".cm-scroller": {
              fontFamily:
                'ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace',
              lineHeight: "1.45",
              minHeight: minH + "px",
            },
            ".cm-content": {
              minHeight: minH - 12 + "px",
              padding: "10px 0",
            },
            "&.cm-focused": {
              outline: "2px solid oklch(0.48 0.14 250)",
            },
          }),
          EditorView.updateListener.of(function (update) {
            if (update.docChanged) {
              onChange(cellId, update.state.doc.toString());
            }
          }),
        ],
      }),
    });

    editors.set(cellId, view);
  });

  return {
    getSource: function (cellId) {
      const v = editors.get(cellId);
      return v ? v.state.doc.toString() : "";
    },
    setSource: function (cellId, text) {
      const v = editors.get(cellId);
      if (!v) return;
      v.dispatch({
        changes: { from: 0, to: v.state.doc.length, insert: text },
      });
    },
    focus: function (cellId) {
      const v = editors.get(cellId);
      if (v) v.focus();
    },
  };
}
