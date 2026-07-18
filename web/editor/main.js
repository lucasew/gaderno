import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { python } from "@codemirror/lang-python";
import { markdown } from "@codemirror/lang-markdown";
import * as Y from "yjs";
import { yCollab } from "y-codemirror.next";
import * as awarenessProtocol from "y-protocols/awareness";
import * as syncProtocol from "y-protocols/sync";
import * as encoding from "lib0/encoding";
import * as decoding from "lib0/decoding";

const COLORS = [
  "#0ea5e9",
  "#8b5cf6",
  "#f59e0b",
  "#10b981",
  "#ef4444",
  "#ec4899",
  "#14b8a6",
  "#f97316",
];

function sourceKey(cellId) {
  return "source:" + cellId;
}

function randomUser() {
  const id =
    Math.random().toString(36).slice(2, 6) +
    Math.random().toString(36).slice(2, 4);
  const color = COLORS[Math.floor(Math.random() * COLORS.length)];
  return {
    name: "user-" + id,
    color,
    colorLight: color + "33",
  };
}

export function createCollabSession() {
  const ydoc = new Y.Doc();
  const awareness = new awarenessProtocol.Awareness(ydoc);
  const user = randomUser();
  awareness.setLocalStateField("user", user);

  const editors = new Map();
  let sendBinary = null;
  let sendJSON = null;
  let connected = false;

  const onDocUpdate = (update, origin) => {
    if (origin === "remote" || !sendBinary) return;
    const encoder = encoding.createEncoder();
    syncProtocol.writeUpdate(encoder, update);
    sendBinary(encoding.toUint8Array(encoder));
  };
  ydoc.on("update", onDocUpdate);

  const onAwareness = ({ added, updated, removed }) => {
    if (!sendJSON) return;
    const changed = added.concat(updated, removed);
    if (changed.length === 0) return;
    const update = awarenessProtocol.encodeAwarenessUpdate(awareness, changed);
    let bin = "";
    for (let i = 0; i < update.length; i++) bin += String.fromCharCode(update[i]);
    sendJSON({ type: "awareness", update: btoa(bin) });
  };
  awareness.on("update", onAwareness);

  function attachTransport(opts) {
    sendBinary = opts.sendBinary;
    sendJSON = opts.sendJSON;
    connected = true;
    const encoder = encoding.createEncoder();
    syncProtocol.writeSyncStep1(encoder, ydoc);
    sendBinary(encoding.toUint8Array(encoder));
    onAwareness({
      added: [awareness.clientID],
      updated: [],
      removed: [],
    });
  }

  function handleSyncMessage(u8) {
    const encoder = encoding.createEncoder();
    const decoder = decoding.createDecoder(u8);
    syncProtocol.readSyncMessage(decoder, encoder, ydoc, "remote");
    if (encoding.length(encoder) > 1 && sendBinary) {
      sendBinary(encoding.toUint8Array(encoder));
    }
  }

  function handleAwarenessB64(b64) {
    try {
      const bin = atob(b64);
      const u8 = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) u8[i] = bin.charCodeAt(i);
      awarenessProtocol.applyAwarenessUpdate(awareness, u8, "remote");
    } catch (_) {}
  }

  function destroyEditors() {
    editors.forEach((v) => v.destroy());
    editors.clear();
  }

  function syncGutterWidth(host, view) {
    if (!host || !view) return;
    const g = view.dom.querySelector(".cm-gutters");
    if (!g) return;
    const w = Math.ceil(g.getBoundingClientRect().width);
    if (w > 0) host.style.setProperty("--cm-gutter-w", w + "px");
  }

  function createEditor(host, cellId, lang) {
    const ytext = ydoc.getText(sourceKey(cellId));
    const langExt = lang === "markdown" ? markdown() : python();
    const minH = lang === "markdown" ? 88 : 72;
    // y-codemirror only observes *future* Y changes — seed CM from Y.Text
    // or remounts look empty even though the CRDT still has content.
    const seed = ytext.toString();
    host.replaceChildren();
    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: seed,
        extensions: [
          basicSetup,
          langExt,
          EditorView.lineWrapping,
          yCollab(ytext, awareness, { undoManager: false }),
          EditorView.theme({
            "&": {
              fontSize: "0.8125rem",
              height: "100%",
              minHeight: minH + "px",
              backgroundColor: "transparent",
              color: "var(--color-base-content)",
            },
            ".cm-scroller": {
              fontFamily:
                'ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace',
              lineHeight: "1.45",
              minHeight: "100%",
              backgroundColor: "transparent",
            },
            ".cm-content": {
              minHeight: minH - 12 + "px",
              padding: "10px 0",
              caretColor: "var(--color-primary)",
            },
            ".cm-gutters": {
              backgroundColor: "transparent",
              color: "color-mix(in oklch, var(--color-base-content) 45%, transparent)",
              borderRight: "none",
            },
            ".cm-activeLineGutter": {
              backgroundColor:
                "color-mix(in oklch, var(--color-primary) 10%, transparent)",
            },
            ".cm-activeLine": {
              backgroundColor:
                "color-mix(in oklch, var(--color-primary) 7%, transparent)",
            },
            "&.cm-focused": {
              outline: "none",
            },
          }),
          // Keep host gutter strip width in sync when line numbers grow (9→10…).
          EditorView.updateListener.of((update) => {
            if (update.docChanged || update.geometryChanged || update.viewportChanged) {
              syncGutterWidth(host, update.view);
            }
          }),
        ],
      }),
    });
    // After first layout paint
    requestAnimationFrame(() => syncGutterWidth(host, view));
    return view;
  }

  // Mount or refresh editors under root. Reuses views whose host DOM is still
  // attached (structure reorder) so typing is not wiped on insert/delete.
  function mountEditors(root) {
    const scope = root || document;
    const seen = new Set();
    const alive = new Set();
    scope.querySelectorAll("[data-gaderno-editor]").forEach((host) => {
      const cellId = host.getAttribute("data-cell-id");
      if (!cellId || seen.has(cellId)) {
        // Skip empty/duplicate ids — they share one Y.Text and cause dual-edit bugs.
        host.replaceChildren();
        host.insertAdjacentHTML(
          "beforeend",
          '<p class="text-xs text-error p-2">Invalid cell id (skipped)</p>'
        );
        return;
      }
      seen.add(cellId);
      alive.add(cellId);

      const existing = editors.get(cellId);
      if (existing && host.contains(existing.dom)) {
        // Same cell host still holds this view (in-place reorder).
        // Re-measure after layout may have shifted.
        try {
          existing.requestMeasure();
        } catch (_) {}
        return;
      }
      if (existing) {
        existing.destroy();
        editors.delete(cellId);
      }
      const lang = host.getAttribute("data-lang") || "python";
      editors.set(cellId, createEditor(host, cellId, lang));
    });

    editors.forEach((view, id) => {
      if (!alive.has(id)) {
        view.destroy();
        editors.delete(id);
      }
    });

    return {
      getSource(cellId) {
        return ydoc.getText(sourceKey(cellId)).toString();
      },
      focus(cellId) {
        const v = editors.get(cellId);
        if (v) v.focus();
      },
      remount(root) {
        return mountEditors(root);
      },
      destroy: destroyEditors,
    };
  }

  function destroy() {
    destroyEditors();
    awareness.off("update", onAwareness);
    ydoc.off("update", onDocUpdate);
    awareness.setLocalState(null);
    ydoc.destroy();
  }

  function setUserName(name) {
    const n = String(name || "").trim();
    if (n) user.name = n;
    awareness.setLocalStateField("user", user);
  }

  return {
    ydoc,
    awareness,
    user,
    setUserName,
    attachTransport,
    handleSyncMessage,
    handleAwarenessB64,
    mountEditors,
    destroy,
    get connected() {
      return connected;
    },
  };
}
