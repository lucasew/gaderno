import { basicSetup } from "codemirror";
import { EditorState, StateField, StateEffect } from "@codemirror/state";
import { EditorView, hoverTooltip, showTooltip } from "@codemirror/view";
import { python } from "@codemirror/lang-python";
import { markdown } from "@codemirror/lang-markdown";
import { autocompletion } from "@codemirror/autocomplete";
import * as Y from "yjs";
import { yCollab } from "y-codemirror.next";
import * as awarenessProtocol from "y-protocols/awareness";
import * as syncProtocol from "y-protocols/sync";
import * as encoding from "lib0/encoding";
import * as decoding from "lib0/decoding";

const hoverTooltipFn = hoverTooltip;
const showTooltipFn = showTooltip;
const StateFieldFn = StateField;
const StateEffectFn = StateEffect;

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

/** True when cursor sits inside unmatched parentheses (call / indexing args). */
function insideParens(text, pos) {
  let depth = 0;
  for (let i = pos - 1; i >= 0; i--) {
    const c = text[i];
    if (c === ")" || c === "]" || c === "}") depth++;
    else if (c === "(" || c === "[" || c === "{") {
      if (depth === 0) return c === "(";
      depth--;
    }
  }
  return false;
}

/** Prefer first non-empty line that looks like a Signature for the args popup. */
function signatureLine(text) {
  if (!text) return "";
  const lines = String(text).split(/\r?\n/);
  for (const line of lines) {
    const t = line.trim();
    if (!t) continue;
    if (/^signature\s*:/i.test(t)) return t.replace(/^signature\s*:\s*/i, "");
    if (/^Init signature\s*:/i.test(t))
      return t.replace(/^Init signature\s*:\s*/i, "");
    if (/^Call signature\s*:/i.test(t))
      return t.replace(/^Call signature\s*:\s*/i, "");
    // First substantive line often is the signature in detail_level=0
    if (t.length < 400) return t;
  }
  return lines[0] || "";
}

function makeInspectDOM(payload, kind) {
  const text =
    typeof payload === "string" ? payload : (payload && payload.text) || "";
  const html = typeof payload === "object" && payload ? payload.html : "";
  const dom = document.createElement("div");
  dom.className =
    "cm-tooltip-hover cm-kernel-inspect" +
    (kind === "signature" ? " cm-kernel-signature" : "");
  if (html && typeof html === "string" && html.indexOf("<pre") !== -1) {
    // Server-built ANSI→HTML only (escaped text + classed spans).
    const wrap = document.createElement("div");
    wrap.className = "cm-kernel-inspect-body cm-kernel-inspect-html";
    wrap.innerHTML = html;
    dom.appendChild(wrap);
  } else {
    const pre = document.createElement("pre");
    pre.className = "cm-kernel-inspect-body";
    pre.textContent = text;
    dom.appendChild(pre);
  }
  return dom;
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
  /** @type {Map<string, { resolve: (v: any) => void, timer: any }>} */
  const pendingRPC = new Map();
  let reqSeq = 0;

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

  function handleRPCReply(msg) {
    if (!msg || !msg.req_id) return;
    const pending = pendingRPC.get(msg.req_id);
    if (!pending) return;
    clearTimeout(pending.timer);
    pendingRPC.delete(msg.req_id);
    pending.resolve(msg);
  }

  function kernelRPC(type, payload, timeoutMs) {
    if (!sendJSON || !connected) return Promise.resolve(null);
    const reqId = "r" + ++reqSeq + "-" + Date.now().toString(36);
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        pendingRPC.delete(reqId);
        resolve(null);
      }, timeoutMs || 4000);
      pendingRPC.set(reqId, { timer, resolve });
      try {
        sendJSON(Object.assign({ type: type, req_id: reqId }, payload));
      } catch (_) {
        clearTimeout(timer);
        pendingRPC.delete(reqId);
        resolve(null);
      }
    });
  }

  function kernelCompletions(context) {
    if (!sendJSON || !connected) return null;
    if (!context.explicit && !context.matchBefore(/[\w.]/)) return null;

    const code = context.state.doc.toString();
    const pos = context.pos;

    return kernelRPC(
      "complete.request",
      { code: code, cursor_pos: pos },
      4000
    ).then((msg) => {
      if (!msg) return null;
      const matches = Array.isArray(msg.matches) ? msg.matches : [];
      if (
        !matches.length ||
        msg.status === "error" ||
        msg.status === "no_kernel"
      ) {
        return null;
      }
      let from = typeof msg.cursor_start === "number" ? msg.cursor_start : pos;
      let to = typeof msg.cursor_end === "number" ? msg.cursor_end : pos;
      const len = code.length;
      if (from < 0) from = 0;
      if (to < from) to = from;
      if (from > len) from = len;
      if (to > len) to = len;
      return {
        from,
        to,
        options: matches.map((m) => ({
          label: String(m),
          type: "variable",
        })),
        validFor: /^[\w.]*$/,
      };
    });
  }

  /** Hover: full inspect (detail_level 1). */
  const kernelHover = hoverTooltipFn(
    async (view, pos) => {
      if (!sendJSON || !connected) return null;
      const code = view.state.doc.toString();
      const msg = await kernelRPC(
        "inspect.request",
        { code: code, cursor_pos: pos, detail_level: 1 },
        4000
      );
      if (!msg || !msg.found || (!msg.text && !msg.html)) return null;
      const text = String(msg.text || "").trim();
      if (!text && !msg.html) return null;
      // Highlight a small span around the hover word when possible.
      let from = pos;
      let to = pos;
      const word = view.state.wordAt(pos);
      if (word) {
        from = word.from;
        to = word.to;
      }
      return {
        pos: from,
        end: to,
        above: true,
        create() {
          return {
            dom: makeInspectDOM(
              { text: text, html: msg.html || "" },
              "hover"
            ),
          };
        },
      };
    },
    { hoverTime: 400 }
  );

  // Signature help state (args popup while typing inside call).
  const setSignature = StateEffectFn.define();
  const signatureField = StateFieldFn.define({
    create() {
      return null;
    },
    update(tooltip, tr) {
      for (const e of tr.effects) {
        if (e.is(setSignature)) return e.value;
      }
      // Clear when leaving paren context or selection jumps away.
      if (tooltip && (tr.docChanged || tr.selection)) {
        const pos = tr.state.selection.main.head;
        const text = tr.state.doc.toString();
        if (!insideParens(text, pos)) return null;
      }
      return tooltip;
    },
    provide: (f) => showTooltipFn.from(f),
  });

  function requestSignature(view) {
    if (!sendJSON || !connected) return;
    const pos = view.state.selection.main.head;
    const code = view.state.doc.toString();
    if (!insideParens(code, pos)) {
      view.dispatch({ effects: setSignature.of(null) });
      return;
    }
    const seq = ++reqSeq;
    kernelRPC(
      "inspect.request",
      { code: code, cursor_pos: pos, detail_level: 0 },
      3500
    ).then((msg) => {
      // Drop stale replies if user kept typing.
      if (!view.dom.isConnected) return;
      const cur = view.state.selection.main.head;
      if (!insideParens(view.state.doc.toString(), cur)) {
        view.dispatch({ effects: setSignature.of(null) });
        return;
      }
      if (!msg || !msg.found || (!msg.text && !msg.html)) {
        view.dispatch({ effects: setSignature.of(null) });
        return;
      }
      // detail_level 0 is already abbreviated; prefer colored HTML when present.
      const line = signatureLine(msg.text || "");
      if (!line && !msg.html) {
        view.dispatch({ effects: setSignature.of(null) });
        return;
      }
      const tip = {
        pos: cur,
        above: true,
        strictSide: true,
        create() {
          return {
            dom: makeInspectDOM(
              {
                text: line || msg.text || "",
                html: msg.html || "",
              },
              "signature"
            ),
          };
        },
      };
      // Ignore if a newer request was issued (reqSeq advanced a lot via other RPCs —
      // use local token instead)
      void seq;
      view.dispatch({ effects: setSignature.of(tip) });
    });
  }

  let sigTimer = null;
  const signaturePlugin = EditorView.updateListener.of((update) => {
    if (!update.docChanged && !update.selectionSet) return;
    const pos = update.state.selection.main.head;
    const text = update.state.doc.toString();
    if (!insideParens(text, pos)) {
      if (update.view.state.field(signatureField, false)) {
        update.view.dispatch({ effects: setSignature.of(null) });
      }
      return;
    }
    // Trigger on typing (, ,, or any edit still inside a call.
    let trigger = update.selectionSet;
    if (update.docChanged) {
      update.changes.iterChanges((_fromA, _toA, _fromB, _toB, inserted) => {
        const s = inserted.toString();
        if (s.includes("(") || s.includes(",") || s.length > 0) trigger = true;
      });
    }
    if (!trigger) return;
    if (sigTimer) clearTimeout(sigTimer);
    sigTimer = setTimeout(() => requestSignature(update.view), 180);
  });

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
    const isPython = lang !== "markdown";
    const langExt = isPython ? python() : markdown();
    const minH = isPython ? 72 : 88;
    const seed = ytext.toString();
    host.replaceChildren();

    const extensions = [
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
      EditorView.updateListener.of((update) => {
        if (
          update.docChanged ||
          update.geometryChanged ||
          update.viewportChanged
        ) {
          syncGutterWidth(host, update.view);
        }
      }),
    ];

    if (isPython) {
      extensions.push(
        autocompletion({
          override: [kernelCompletions],
          activateOnTyping: true,
          maxRenderedOptions: 50,
        }),
        kernelHover,
        signatureField,
        signaturePlugin
      );
    }

    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: seed,
        extensions,
      }),
    });
    requestAnimationFrame(() => syncGutterWidth(host, view));
    return view;
  }

  function mountEditors(root) {
    const scope = root || document;
    const seen = new Set();
    const alive = new Set();
    scope.querySelectorAll("[data-gaderno-editor]").forEach((host) => {
      const cellId = host.getAttribute("data-cell-id");
      if (!cellId || seen.has(cellId)) {
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
    if (sigTimer) clearTimeout(sigTimer);
    pendingRPC.forEach((p) => {
      clearTimeout(p.timer);
      p.resolve(null);
    });
    pendingRPC.clear();
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
    handleCompleteReply: handleRPCReply,
    handleInspectReply: handleRPCReply,
    handleRPCReply,
    mountEditors,
    destroy,
    get connected() {
      return connected;
    },
  };
}
