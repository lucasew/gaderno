import { createCollabSession } from "./editor.js";

(function () {
  "use strict";

  function $(sel, root) {
    return (root || document).querySelector(sel);
  }
  function $all(sel, root) {
    return Array.from((root || document).querySelectorAll(sel));
  }

  const cfg = window.__GADERNO__ || {};
  const path = cfg.path || "";
  const sessionDot = $("#session-dot");
  const sessionLabel = $("#session-label");
  const btnSession = $("#btn-session");

  const NAME_KEY = "gaderno-display-name";
  const collab = createCollabSession();
  let api = null;
  let ws = null;

  // Percent-encode each path segment so "My Notebook.ipynb" works in /ws/… URLs.
  function encodeNotebookPath(p) {
    return String(p || "")
      .split("/")
      .map(function (seg) {
        return encodeURIComponent(seg);
      })
      .join("/");
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function kernelLabel() {
    if (!kernelStatus || kernelStatus.needs_kernel || !kernelStatus.bound_name)
      return "";
    return (
      kernelStatus.display_name ||
      kernelStatus.bound_name ||
      ""
    ).trim();
  }

  /** e.g. "Live · Python 3" when a kernel is bound */
  function withKernel(text) {
    const k = kernelLabel();
    if (!k || !text) return text || "";
    // Avoid doubling if caller already appended the name
    if (text.indexOf(k) !== -1) return text;
    return text + " · " + k;
  }

  function setStatus(text, state) {
    if (sessionDot) sessionDot.setAttribute("data-state", state || "off");
    if (sessionLabel) sessionLabel.textContent = text || "";
    if (btnSession) {
      const needs =
        kernelStatus && (kernelStatus.needs_kernel || !kernelStatus.bound_name);
      let title = text || "Session";
      if (needs) title = (text || "Session") + " · choose kernel";
      else if (kernelLabel() && title.indexOf(kernelLabel()) === -1)
        title = withKernel(text || "Session");
      btnSession.title = title;
      btnSession.setAttribute("aria-label", title);
    }
  }

  function setLiveStatus(state) {
    setStatus(withKernel("Live"), state || "ok");
  }

  function sendJSON(obj) {
    if (!ws || ws.readyState !== 1) return false;
    ws.send(JSON.stringify(obj));
    return true;
  }

  function sendBinary(u8) {
    if (!ws || ws.readyState !== 1) return false;
    ws.send(u8);
    return true;
  }

  function flushSource(cellId) {
    if (!api) return "";
    return api.getSource(cellId);
  }

  // —— Display name / avatar ——
  function getDisplayName() {
    try {
      return (localStorage.getItem(NAME_KEY) || "").trim();
    } catch (_) {
      return "";
    }
  }

  function setDisplayName(name) {
    const n = String(name || "").trim().slice(0, 40);
    try {
      if (n) localStorage.setItem(NAME_KEY, n);
      else localStorage.removeItem(NAME_KEY);
    } catch (_) {}
    updateAvatar(n);
    if (api && typeof collab.setUserName === "function") {
      collab.setUserName(n || undefined);
    }
  }

  function updateAvatar(name) {
    const el = $("#user-avatar");
    if (!el) return;
    const n = (name || getDisplayName() || "?").trim();
    el.textContent = (n[0] || "?").toUpperCase();
  }

  const nameInput = $("#display-name");
  if (nameInput) {
    nameInput.value = getDisplayName();
    nameInput.addEventListener("change", function () {
      setDisplayName(nameInput.value);
    });
    nameInput.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        e.preventDefault();
        setDisplayName(nameInput.value);
        const d = nameInput.closest("details");
        if (d) d.open = false;
      }
    });
  }
  updateAvatar();

  $all(".theme-set").forEach(function (b) {
    b.addEventListener("click", function () {
      const t = b.getAttribute("data-theme");
      if (window.gadernoSetTheme) window.gadernoSetTheme(t);
      const d = b.closest("details");
      if (d) d.open = false;
    });
  });

  // Per-tab hub lifetime fence (SPEC: session identity).
  const sessionStorageKey = "gaderno.session:" + path;
  let sessionReady = false;

  async function connect() {
    if (!path) return;
    sessionReady = false;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    let url =
      proto + "://" + location.host + "/ws/notebooks/" + encodeNotebookPath(path);
    // SPEC: short-lived ticket when shared token is set (browsers cannot set Authorization on WS).
    // Cookie auth also works; ticket is preferred and one-shot.
    try {
      const tr = await fetch("/api/ws-ticket", {
        method: "POST",
        credentials: "same-origin",
      });
      if (tr.ok) {
        const tj = await tr.json();
        if (tj && tj.ticket) {
          url += (url.indexOf("?") >= 0 ? "&" : "?") + "ticket=" + encodeURIComponent(tj.ticket);
        }
      } else if (tr.status === 401) {
        setStatus("Auth required", "err");
        return;
      }
    } catch (_) {
      /* open without ticket when endpoint unreachable */
    }
    const sock = new WebSocket(url);
    ws = sock;
    sock.binaryType = "arraybuffer";
    sock.onopen = function () {
      setStatus("Connecting", "run");
    };
    sock.onclose = function () {
      if (ws !== sock) return;
      sessionReady = false;
      setStatus("Offline", "off");
      setTimeout(connect, 1500);
    };
    sock.onerror = function () {
      if (ws !== sock) return;
      setStatus("Error", "err");
    };
    sock.onmessage = function (ev) {
      if (ws !== sock) return;
      if (ev.data instanceof ArrayBuffer) {
        if (!sessionReady) return;
        collab.handleSyncMessage(new Uint8Array(ev.data));
        return;
      }
      if (typeof ev.data !== "string") return;
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (_) {
        return;
      }
      if (msg.type === "hello") {
        const prev = sessionStorage.getItem(sessionStorageKey);
        const sid = msg.session_id || "";
        if (prev && sid && prev !== sid) {
          sessionStorage.setItem(sessionStorageKey, sid);
          try {
            sock.close();
          } catch (_) {}
          location.reload();
          return;
        }
        if (sid) sessionStorage.setItem(sessionStorageKey, sid);
        sessionReady = true;
        sendJSON({ type: "hello.ack", session_id: sid });
        collab.attachTransport({
          sendBinary: sendBinary,
          sendJSON: sendJSON,
        });
        const dn = getDisplayName();
        if (dn && typeof collab.setUserName === "function") collab.setUserName(dn);
        setLiveStatus("ok");
        applyKernelStatus(kernelStatus);
        return;
      }
      if (!sessionReady) return;
      if (msg.type === "awareness" && msg.update) {
        collab.handleAwarenessB64(msg.update);
      } else if (msg.type === "notebook.structure") {
        applyStructure(msg.cells || []);
      } else if (msg.type === "exec.clear") {
        const cell = document.querySelector(
          '.cell-row[data-cell-id="' + msg.cell_id + '"]'
        );
        if (!cell) return;
        clearCellOutput(cell, true);
      } else if (msg.type === "exec.stream") {
        const cell = document.querySelector(
          '.cell-row[data-cell-id="' + msg.cell_id + '"]'
        );
        if (!cell) return;
        if (!cell._streamBuf) cell._streamBuf = { stdout: "", stderr: "" };
        const name = msg.name === "stderr" ? "stderr" : "stdout";
        cell._streamBuf[name] = msg.text || "";
        renderStreams(cell, true);
      } else if (msg.type === "exec.display") {
        const cell = document.querySelector(
          '.cell-row[data-cell-id="' + msg.cell_id + '"]'
        );
        if (!cell) return;
        appendDisplay(cell, msg);
      } else if (msg.type === "exec.result") {
        const cell = document.querySelector(
          '.cell-row[data-cell-id="' + msg.cell_id + '"]'
        );
        if (!cell) return;
        cell._streamBuf = {
          stdout: msg.stdout || "",
          stderr: msg.stderr || "",
        };
        const out = ensureOutBlock(cell);
        const countEl = $(".cell-exec-count", cell);
        const play = $(".cell-play", cell);
        cell.classList.remove("is-running");
        if (msg.status === "error") cell.classList.add("is-error");
        else cell.classList.remove("is-error");
        if (out) {
          out.hidden = false;
          out.classList.remove("is-running");
          if (msg.status === "error") out.classList.add("is-error");
          else out.classList.remove("is-error");
        }
        renderStreams(cell, false);
        if (msg.status === "error") {
          // Prefer full traceback (ANSI already stripped server-side); fall
          // back to ename:evalue when the kernel omitted frames.
          let errText = "";
          if (Array.isArray(msg.traceback) && msg.traceback.length) {
            errText = msg.traceback.join("\n");
          } else {
            errText = (msg.ename || "Error") + ": " + (msg.evalue || "");
          }
          setErrorLine(cell, errText);
        } else {
          clearErrorLine(cell);
        }
        // Empty success with no streams/displays: keep block minimal
        if (out && !out.children.length) {
          out.hidden = true;
        }
        if (countEl && msg.execution_count != null) {
          countEl.textContent = "[" + msg.execution_count + "]";
          countEl.setAttribute("data-count", String(msg.execution_count));
        }
        setLiveStatus("ok");
        if (play) {
          play.disabled = false;
          play.classList.remove("is-running");
        }
      } else if (msg.type === "error") {
        setStatus(msg.text || "Error", "err");
        $all("button.cell-play").forEach(function (b) {
          b.disabled = false;
          b.classList.remove("is-running");
        });
        $all(".cell-row.is-running").forEach(function (c) {
          c.classList.remove("is-running");
        });
      } else if (msg.type === "kernel.status") {
        applyKernelStatus(msg.status);
      } else if (msg.type === "kernel.needs_pick") {
        openKernelChooser();
      } else if (msg.type === "chat.message") {
        const log = $("#chat-log");
        if (!log) return;
        const line = document.createElement("div");
        line.className = "py-1";
        const who = document.createElement("span");
        who.className = "font-code font-semibold text-primary mr-1.5";
        who.textContent = msg.from || "?";
        line.appendChild(who);
        line.appendChild(document.createTextNode(msg.text || ""));
        log.appendChild(line);
        log.scrollTop = log.scrollHeight;
      } else if (
        msg.type === "complete.reply" ||
        msg.type === "inspect.reply"
      ) {
        if (typeof collab.handleRPCReply === "function") {
          collab.handleRPCReply(msg);
        } else if (typeof collab.handleCompleteReply === "function") {
          collab.handleCompleteReply(msg);
        }
      }
    };
  }

  // —— Cell outputs (streams + mime displays) ——
  function trustKey() {
    return "gaderno.trusted:" + (path || "");
  }
  function isNotebookTrusted() {
    try {
      return localStorage.getItem(trustKey()) === "1";
    } catch (_) {
      return false;
    }
  }
  function setNotebookTrusted(on) {
    try {
      if (on) localStorage.setItem(trustKey(), "1");
      else localStorage.removeItem(trustKey());
    } catch (_) {}
    // Re-render HTML displays under current trust.
    $all(".out-display[data-has-html='1']").forEach(function (el) {
      const raw = el.getAttribute("data-html");
      const host = $(".out-display-html-slot", el);
      if (!host) return;
      host.replaceChildren();
      if (on && raw) {
        const box = document.createElement("div");
        box.className = "out-display-html";
        box.innerHTML = raw;
        host.appendChild(box);
      } else if (raw) {
        const gate = document.createElement("div");
        gate.className = "out-display-gated";
        gate.textContent =
          "HTML output hidden — enable “Trust HTML outputs” in the menu.";
        host.appendChild(gate);
      }
    });
  }

  function ensureOutBlock(cell) {
    if (!cell) return null;
    let out = $(".out-block", cell);
    if (!out) {
      out = document.createElement("div");
      out.className = "out-block";
      out.hidden = true;
      const body = $(".cell-body", cell) || cell;
      body.appendChild(out);
    }
    return out;
  }

  function clearCellOutput(cell, running) {
    cell._streamBuf = { stdout: "", stderr: "" };
    const out = ensureOutBlock(cell);
    if (!out) return;
    out.replaceChildren();
    out.hidden = false;
    out.classList.remove("is-error");
    if (running) out.classList.add("is-running");
    else out.classList.remove("is-running");
  }

  function ensureStreamEl(out, kind) {
    let el = $(":scope > .out-stream.out-" + kind, out);
    if (!el) {
      el = document.createElement("pre");
      el.className = "out-stream out-" + kind;
      // streams first: insert before first display if any
      const firstDisplay = $(":scope > .out-display", out);
      if (firstDisplay) out.insertBefore(el, firstDisplay);
      else out.appendChild(el);
    }
    return el;
  }

  function renderStreams(cell, running) {
    const out = ensureOutBlock(cell);
    if (!out) return;
    out.hidden = false;
    if (running) out.classList.add("is-running");
    const buf = cell._streamBuf || { stdout: "", stderr: "" };
    if (buf.stdout) {
      const el = ensureStreamEl(out, "stdout");
      el.textContent = buf.stdout;
    } else {
      const el = $(":scope > .out-stream.out-stdout", out);
      if (el) el.remove();
    }
    if (buf.stderr) {
      const el = ensureStreamEl(out, "stderr");
      el.textContent = buf.stderr;
    } else {
      const el = $(":scope > .out-stream.out-stderr", out);
      if (el) el.remove();
    }
  }

  function setErrorLine(cell, text) {
    const out = ensureOutBlock(cell);
    if (!out) return;
    out.hidden = false;
    let el = $(":scope > .out-stream.out-error", out);
    if (!el) {
      el = document.createElement("pre");
      el.className = "out-stream out-error";
      out.appendChild(el);
    }
    el.textContent = text || "";
  }

  function clearErrorLine(cell) {
    const out = $(".out-block", cell);
    if (!out) return;
    const el = $(":scope > .out-stream.out-error", out);
    if (el) el.remove();
  }

  function mimeString(data, mime) {
    if (!data || data[mime] == null) return "";
    const v = data[mime];
    if (typeof v === "string") return v;
    if (Array.isArray(v)) return v.join("");
    return String(v);
  }

  function stripDataURLPrefix(s) {
    // Some kernels already send data:image/png;base64,...
    const m = /^data:[^;]+;base64,(.+)$/i.exec(s);
    return m ? m[1] : s;
  }

  function appendDisplay(cell, msg) {
    const out = ensureOutBlock(cell);
    if (!out) return;
    out.hidden = false;
    out.classList.add("is-running");
    const data = msg.data || {};
    const wrap = document.createElement("div");
    wrap.className = "out-display";

    const png = mimeString(data, "image/png");
    const jpeg = mimeString(data, "image/jpeg") || mimeString(data, "image/jpg");
    const gif = mimeString(data, "image/gif");
    const svg = mimeString(data, "image/svg+xml");
    const plain = mimeString(data, "text/plain");
    const html = mimeString(data, "text/html");

    let renderedRich = false;

    if (png || jpeg || gif) {
      const img = document.createElement("img");
      img.alt = plain || "output image";
      if (png) img.src = "data:image/png;base64," + stripDataURLPrefix(png);
      else if (jpeg)
        img.src = "data:image/jpeg;base64," + stripDataURLPrefix(jpeg);
      else img.src = "data:image/gif;base64," + stripDataURLPrefix(gif);
      wrap.appendChild(img);
      renderedRich = true;
    } else if (svg) {
      // SVG as image via data URL (safer than inline for untrusted)
      const img = document.createElement("img");
      img.alt = plain || "output svg";
      try {
        img.src =
          "data:image/svg+xml;base64," +
          btoa(unescape(encodeURIComponent(svg)));
      } catch (_) {
        img.src =
          "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
      }
      wrap.appendChild(img);
      renderedRich = true;
    }

    if (html) {
      wrap.setAttribute("data-has-html", "1");
      wrap.setAttribute("data-html", html);
      const slot = document.createElement("div");
      slot.className = "out-display-html-slot";
      if (isNotebookTrusted()) {
        const box = document.createElement("div");
        box.className = "out-display-html";
        box.innerHTML = html;
        slot.appendChild(box);
        renderedRich = true;
      } else {
        const gate = document.createElement("div");
        gate.className = "out-display-gated";
        gate.textContent =
          "HTML output hidden — enable “Trust HTML outputs” in the menu.";
        slot.appendChild(gate);
      }
      wrap.appendChild(slot);
    }

    // Always show text/plain when present (alongside images), except pure
    // matplotlib figure placeholders if we already rendered an image.
    if (plain) {
      const isFigPlaceholder =
        renderedRich && /^<Figure\b/i.test(plain.trim());
      if (!isFigPlaceholder) {
        const pre = document.createElement("pre");
        pre.className = "out-display-plain";
        pre.textContent = plain;
        wrap.appendChild(pre);
      }
    }

    if (!wrap.children.length) {
      const pre = document.createElement("pre");
      pre.className = "out-display-plain";
      pre.textContent =
        "(" + (msg.output_type || "display") + ": no renderable mime)";
      wrap.appendChild(pre);
    }

    out.appendChild(wrap);
  }

  function insertGapHTML(beforeId) {
    const attr = beforeId
      ? ' data-insert-before="' + escapeHtml(beforeId) + '"'
      : " data-insert-end";
    return (
      '<div class="cell-insert' +
      (beforeId ? "" : " cell-insert-end") +
      '"' +
      attr +
      ' role="group" aria-label="Insert cell">' +
      '<button type="button" class="cell-insert-btn" data-type="code" title="Insert code cell" aria-label="Insert code cell">' +
      '<span aria-hidden="true">+</span><span class="cell-insert-label">Code</span></button>' +
      '<button type="button" class="cell-insert-btn" data-type="markdown" title="Insert markdown cell" aria-label="Insert markdown cell">' +
      '<span aria-hidden="true">+</span><span class="cell-insert-label">Markdown</span></button>' +
      "</div>"
    );
  }

  function buildCellHTML(c) {
    const id = escapeHtml(c.id);
    const typ = c.type === "markdown" ? "markdown" : "code";
    const isCode = typ === "code";
    let html = "";
    html +=
      '<article class="cell-row" data-cell-id="' +
      id +
      '" data-cell-type="' +
      typ +
      '">';
    html += '<div class="cell-gutter">';
    if (isCode) {
      html +=
        '<button type="button" class="cell-play run" data-cell-id="' +
        id +
        '" title="Run cell" aria-label="Run cell">' +
        '<svg class="play-icon" width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8 5v14l11-7z"/></svg>' +
        '<span class="loading loading-spinner loading-xs play-spin" hidden></span>' +
        "</button>" +
        '<span class="cell-exec-count font-code tabular" data-count></span>';
    } else {
      html +=
        '<span class="cell-md-mark" title="Markdown" aria-hidden="true">¶</span>';
    }
    html += "</div>";
    html += '<div class="cell-body min-w-0">';
    html +=
      '<div class="cell-toolbar"><span class="flex-1"></span>' +
      '<details class="dropdown dropdown-end cell-menu">' +
      '<summary class="g-icon-btn g-icon-btn-sm" title="Cell menu" aria-label="Cell menu">' +
      '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="19" cy="12" r="1.5"/></svg>' +
      "</summary>" +
      '<ul class="menu dropdown-content bg-base-100 rounded-box z-50 w-44 p-1 shadow-md border border-base-300 text-xs">' +
      '<li><button type="button" class="cell-type-set" data-cell-id="' +
      id +
      '" data-type="code">Type: Code</button></li>' +
      '<li><button type="button" class="cell-type-set" data-cell-id="' +
      id +
      '" data-type="markdown">Type: Markdown</button></li>' +
      '<li><hr class="my-0.5 border-base-300"></li>' +
      '<li><button type="button" class="cell-up" data-cell-id="' +
      id +
      '">Move up</button></li>' +
      '<li><button type="button" class="cell-down" data-cell-id="' +
      id +
      '">Move down</button></li>' +
      '<li><hr class="my-0.5 border-base-300"></li>' +
      '<li><button type="button" class="cell-del text-error" data-cell-id="' +
      id +
      '">Delete</button></li>' +
      "</ul></details></div>";
    if (!isCode) {
      html +=
        '<div class="md-preview" tabindex="0" role="button" aria-label="Edit markdown"></div>';
    }
    html +=
      '<div class="cm-host' +
      (isCode ? "" : " is-md-edit") +
      '" data-gaderno-editor data-cell-id="' +
      id +
      '" data-lang="' +
      (isCode ? "python" : "markdown") +
      '"' +
      (isCode ? "" : " hidden") +
      "></div>";
    if (isCode) {
      html += '<div class="out-block" hidden></div>';
    }
    html += "</div></article>";
    return html;
  }

  function syncMarkdownPreviews(root) {
    if (!api) return;
    $all('.cell-row[data-cell-type="markdown"]', root || document).forEach(
      function (cell) {
        const id = cell.getAttribute("data-cell-id");
        const preview = $(".md-preview", cell);
        const host = $("[data-gaderno-editor]", cell);
        if (!preview || !host || !host.hidden) return;
        preview.textContent = api.getSource(id) || "";
      }
    );
  }

  function enterMarkdownEdit(cell) {
    if (!cell || !api) return;
    const id = cell.getAttribute("data-cell-id");
    const preview = $(".md-preview", cell);
    const host = $("[data-gaderno-editor]", cell);
    if (!preview || !host) return;
    preview.hidden = true;
    host.hidden = false;
    api.focus(id);
  }

  function exitMarkdownEdit(cell) {
    if (!cell || !api) return;
    const id = cell.getAttribute("data-cell-id");
    const preview = $(".md-preview", cell);
    const host = $("[data-gaderno-editor]", cell);
    if (!preview || !host) return;
    preview.textContent = api.getSource(id) || "";
    preview.hidden = false;
    host.hidden = true;
  }

  function rebuildInsertGaps(root) {
    // Remove existing gaps then reinsert around cells
    $all(".cell-insert", root).forEach(function (g) {
      g.remove();
    });
    const rows = $all(":scope > .cell-row", root);
    if (rows.length === 0) return;
    rows.forEach(function (row) {
      const id = row.getAttribute("data-cell-id");
      const tmp = document.createElement("div");
      tmp.innerHTML = insertGapHTML(id);
      const gap = tmp.firstElementChild;
      if (gap) root.insertBefore(gap, row);
    });
    const tmpEnd = document.createElement("div");
    tmpEnd.innerHTML = insertGapHTML(null);
    const end = tmpEnd.firstElementChild;
    if (end) root.appendChild(end);
  }

  function applyStructure(cells) {
    const root = document.getElementById("cells");
    if (!root || !Array.isArray(cells)) return;
    const seen = new Set();
    const unique = [];
    cells.forEach(function (c) {
      if (!c || !c.id || seen.has(c.id)) return;
      seen.add(c.id);
      unique.push(c);
    });

    $all(":scope > :not(.cell-row)", root).forEach(function (el) {
      el.remove();
    });

    if (unique.length === 0) {
      root.innerHTML =
        '<div class="g-empty g-empty-cells" id="empty-notebook">' +
        '<p class="font-medium">Empty notebook</p>' +
        '<p class="text-sm text-base-content/55 mt-1 mb-4">Add a first cell to begin.</p>' +
        '<div class="flex flex-wrap gap-2 justify-center">' +
        '<button type="button" class="btn btn-primary btn-sm gap-1" id="btn-first-code"><span aria-hidden="true">+</span> Code</button>' +
        '<button type="button" class="btn btn-ghost btn-sm gap-1" id="btn-first-md"><span aria-hidden="true">+</span> Markdown</button>' +
        "</div></div>";
      api = collab.mountEditors(root);
      return;
    }

    const byId = new Map();
    $all(".cell-row", root).forEach(function (el) {
      const id = el.getAttribute("data-cell-id");
      if (id) byId.set(id, el);
    });

    unique.forEach(function (c) {
      const typ = c.type === "markdown" ? "markdown" : "code";
      let el = byId.get(c.id);
      if (el && el.getAttribute("data-cell-type") === typ) return;
      if (el) {
        el.remove();
        byId.delete(c.id);
      }
      const tmp = document.createElement("div");
      tmp.innerHTML = buildCellHTML(c);
      el = tmp.firstElementChild;
      if (!el) return;
      root.appendChild(el);
      byId.set(c.id, el);
    });

    const want = new Set(
      unique.map(function (c) {
        return c.id;
      })
    );
    byId.forEach(function (el, id) {
      if (!want.has(id)) {
        el.remove();
        byId.delete(id);
      }
    });

    unique.forEach(function (c, i) {
      const el = byId.get(c.id);
      if (!el || el.parentNode !== root) return;
      // children include only cell-rows at this point
      const rows = $all(":scope > .cell-row", root);
      const ref = rows[i];
      if (ref !== el) {
        root.insertBefore(el, ref || null);
      }
    });

    rebuildInsertGaps(root);
    api = collab.mountEditors(root);
    syncMarkdownPreviews(root);
  }

  // Initial mount
  api = collab.mountEditors(document.getElementById("cells") || document);
  syncMarkdownPreviews(document.getElementById("cells"));

  // Seed markdown previews from SSR JSON if collab not yet filled
  $all(".cell-row[data-cell-type='markdown']").forEach(function (cell) {
    const id = cell.getAttribute("data-cell-id");
    const preview = $(".md-preview", cell);
    const jsonEl = $('.cell-source-json[data-cell-id="' + id + '"]', cell) ||
      $('.cell-source-json[data-cell-id="' + id + '"]');
    if (!preview || preview.textContent) return;
    if (jsonEl) {
      try {
        preview.textContent = JSON.parse(jsonEl.textContent || '""') || "";
      } catch (_) {}
    }
  });

  function closeMenus() {
    document.querySelectorAll("details.dropdown[open]").forEach(function (d) {
      d.open = false;
    });
  }

  function insertCell(type, index) {
    sendJSON({ type: "cell.insert", text: type, index: index });
  }

  function indexOfCell(id) {
    const rows = $all("#cells .cell-row");
    return rows.findIndex(function (r) {
      return r.getAttribute("data-cell-id") === id;
    });
  }

  document.addEventListener("click", function (e) {
    const play = e.target.closest("button.cell-play, button.run");
    if (play) {
      const id = play.dataset.cellId;
      if (!id || !ws || ws.readyState !== 1) {
        setStatus("Not connected", "err");
        return;
      }
      play.disabled = true;
      play.classList.add("is-running");
      setStatus("Running", "run");
      const source = flushSource(id);
      const cell = play.closest(".cell-row");
      if (cell) cell.classList.add("is-running");
      if (cell) clearCellOutput(cell, true);
      sendJSON({ type: "exec.run", cell_id: id, source: source });
      return;
    }

    const mdPreview = e.target.closest(".md-preview");
    if (mdPreview) {
      enterMarkdownEdit(mdPreview.closest(".cell-row"));
      return;
    }

    const typeSet = e.target.closest("button.cell-type-set");
    if (typeSet) {
      const id = typeSet.dataset.cellId;
      const typ = typeSet.dataset.type;
      if (!id || !typ) return;
      closeMenus();
      sendJSON({ type: "cell.set_type", cell_id: id, text: typ });
      return;
    }

    const insertBtn = e.target.closest(".cell-insert-btn");
    if (insertBtn) {
      const typ = insertBtn.getAttribute("data-type") || "code";
      const gap = insertBtn.closest(".cell-insert");
      if (!gap) return;
      if (gap.hasAttribute("data-insert-end")) {
        insertCell(typ, $all("#cells .cell-row").length);
      } else {
        const before = gap.getAttribute("data-insert-before");
        const idx = indexOfCell(before);
        insertCell(typ, idx >= 0 ? idx : 0);
      }
      return;
    }

    if (e.target.closest("#btn-first-code")) {
      insertCell("code", 0);
      return;
    }
    if (e.target.closest("#btn-first-md")) {
      insertCell("markdown", 0);
      return;
    }

    const del = e.target.closest(".cell-del");
    if (del) {
      const id = del.dataset.cellId;
      if (!id) return;
      if (!confirm("Delete this cell?")) return;
      closeMenus();
      sendJSON({ type: "cell.delete", cell_id: id });
      return;
    }
    const up = e.target.closest(".cell-up");
    if (up) {
      const id = up.dataset.cellId;
      const idx = indexOfCell(id);
      closeMenus();
      if (idx > 0) sendJSON({ type: "cell.move", cell_id: id, index: idx - 1 });
      return;
    }
    const down = e.target.closest(".cell-down");
    if (down) {
      const id = down.dataset.cellId;
      const rows = $all("#cells .cell-row");
      const idx = indexOfCell(id);
      closeMenus();
      if (idx >= 0 && idx < rows.length - 1)
        sendJSON({ type: "cell.move", cell_id: id, index: idx + 1 });
      return;
    }
  });

  // Blur markdown editor → preview
  document.addEventListener(
    "focusout",
    function (e) {
      const host = e.target.closest && e.target.closest(".cm-host.is-md-edit, .cm-host[data-lang='markdown']");
      if (!host) return;
      const cell = host.closest(".cell-row");
      if (!cell || cell.getAttribute("data-cell-type") !== "markdown") return;
      // Defer so click-into-toolbar still works
      setTimeout(function () {
        if (cell.contains(document.activeElement)) return;
        if (host.hidden) return;
        exitMarkdownEdit(cell);
      }, 120);
    },
    true
  );

  // —— Chat panel ——
  const chatPanel = document.getElementById("chat-panel");
  const btnChat = document.getElementById("btn-chat");
  const btnChatClose = document.getElementById("btn-chat-close");
  const CHAT_KEY = "gaderno-chat-open";

  function setChatOpen(open) {
    if (!chatPanel) return;
    chatPanel.dataset.open = open ? "true" : "false";
    chatPanel.setAttribute("aria-hidden", open ? "false" : "true");
    if (btnChat) {
      btnChat.setAttribute("aria-expanded", open ? "true" : "false");
      btnChat.classList.toggle("g-active", open);
    }
    try {
      localStorage.setItem(CHAT_KEY, open ? "1" : "0");
    } catch (_) {}
    if (open) {
      const input = document.getElementById("chat-input");
      if (input) setTimeout(function () {
        input.focus();
      }, 200);
    }
  }

  function toggleChat() {
    if (!chatPanel) return;
    setChatOpen(chatPanel.dataset.open !== "true");
  }

  if (btnChat) btnChat.addEventListener("click", toggleChat);
  if (btnChatClose)
    btnChatClose.addEventListener("click", function () {
      setChatOpen(false);
    });
  try {
    setChatOpen(localStorage.getItem(CHAT_KEY) === "1");
  } catch (_) {
    setChatOpen(false);
  }

  const chatForm = $("#chat-form");
  if (chatForm) {
    chatForm.addEventListener("submit", function (e) {
      e.preventDefault();
      const input = $("#chat-input");
      const text = ((input && input.value) || "").trim();
      if (!text || !ws || ws.readyState !== 1) return;
      sendJSON({ type: "chat.send", text: text });
      input.value = "";
    });
  }

  // —— Kernel chooser ——
  const kernelDialog = document.getElementById("kernel-dialog");
  const kernelList = document.getElementById("kernel-list");
  const kernelFilter = document.getElementById("kernel-filter");
  const kernelDialogHint = document.getElementById("kernel-dialog-hint");
  let kernelStatus = {
    needs_kernel: true,
    bound_name: "",
    display_name: "",
    phase: "needs_kernel",
    running: false,
  };
  let autoOpenedChooser = false;
  let kernelCatalog = null;

  function applyKernelStatus(st) {
    if (!st) return;
    kernelStatus = st;
    const needs = st.needs_kernel || !st.bound_name;
    // Don't override live/offline connection label unless connected
    if (sessionReady) {
      if (needs) setStatus("Pick kernel", "warn");
      else if (st.phase === "busy" || st.running)
        setStatus(withKernel("Busy"), "run");
      else if (st.phase === "starting")
        setStatus(withKernel("Starting"), "run");
      else if (st.phase === "dead")
        setStatus(withKernel("Kernel error"), "err");
      else setLiveStatus("ok");
    }
    if (needs && kernelDialog && !kernelDialog.open && !autoOpenedChooser) {
      autoOpenedChooser = true;
      openKernelChooser();
    }
  }

  function renderKernelList(filter) {
    if (!kernelList) return;
    const q = (filter || "").trim().toLowerCase();
    const groups = (kernelCatalog && kernelCatalog.groups) || {};
    const order = ["jupyter", "uv"];
    const titles = { jupyter: "Jupyter", uv: "uv" };
    const blurb = {
      jupyter: "Installed kernelspecs on this machine",
      uv: "Managed by uv (starts with ipykernel on first play)",
    };

    let html = "";
    let shown = 0;
    order.forEach(function (g) {
      let items = groups[g] || [];
      if (q) {
        items = items.filter(function (k) {
          const hay = (
            (k.display_name || "") +
            " " +
            (k.name || "") +
            " " +
            (k.language || "")
          ).toLowerCase();
          return hay.indexOf(q) >= 0;
        });
      }
      if (!items.length) return;
      html += '<div class="px-1 pt-2 first:pt-1">';
      html +=
        '<div class="px-2 pb-1">' +
        '<div class="text-[0.65rem] font-semibold uppercase tracking-wide text-base-content/45">' +
        titles[g] +
        "</div>" +
        '<div class="text-[0.65rem] text-base-content/40 leading-snug">' +
        blurb[g] +
        "</div></div>";
      html += '<ul class="menu menu-sm p-0 gap-0 rounded-none w-full">';
      items.forEach(function (k) {
        shown++;
        const active = kernelStatus.bound_name === k.name;
        const name = escapeHtml(k.name);
        const disp = escapeHtml(k.display_name || k.name);
        const lang = escapeHtml(k.language || "");
        html += '<li class="w-full">';
        html +=
          '<button type="button" class="kernel-pick w-full rounded-none' +
          (active ? " menu-active" : "") +
          '" data-name="' +
          name +
          '">';
        html += '<div class="flex items-center gap-2 w-full min-w-0 py-0.5">';
        html +=
          '<span class="flex h-4 w-4 shrink-0 items-center justify-center text-[0.7rem]" aria-hidden="true">' +
          (active ? "✓" : "") +
          "</span>";
        html += '<div class="min-w-0 flex-1 text-left">';
        html +=
          '<div class="truncate text-xs font-medium leading-tight">' +
          disp +
          "</div>";
        html +=
          '<div class="truncate font-code text-[0.65rem] text-base-content/45 leading-tight">' +
          name +
          "</div>";
        html += "</div>";
        if (lang) {
          html +=
            '<span class="badge badge-ghost badge-xs shrink-0 font-normal">' +
            lang +
            "</span>";
        }
        html += "</div></button></li>";
      });
      html += "</ul></div>";
    });

    if (!shown) {
      html =
        '<div class="px-4 py-10 text-center text-xs text-base-content/50">' +
        (q
          ? "No kernels match “" + escapeHtml(q) + "”."
          : "No kernels found. Install a Jupyter kernelspec or uv.") +
        "</div>";
    }
    kernelList.innerHTML = html;
    if (kernelDialogHint) {
      kernelDialogHint.textContent = shown
        ? shown + " available"
        : "Nothing to show";
    }
  }

  async function openKernelChooser() {
    if (!kernelDialog || !kernelList) return;
    if (kernelFilter) kernelFilter.value = "";
    kernelList.innerHTML =
      '<div class="flex items-center justify-center gap-2 py-10 text-base-content/50 text-xs">' +
      '<span class="loading loading-spinner loading-xs"></span>Loading kernels…</div>';
    kernelDialog.showModal();
    if (kernelFilter)
      setTimeout(function () {
        kernelFilter.focus();
      }, 50);
    try {
      const r = await fetch("/api/kernels");
      if (!r.ok) throw new Error("load failed");
      kernelCatalog = await r.json();
      renderKernelList("");
    } catch (e) {
      kernelList.innerHTML =
        '<div class="px-4 py-10 text-center text-xs text-error">Could not load kernels.</div>';
    }
  }

  if (btnSession) {
    btnSession.addEventListener("click", function () {
      openKernelChooser();
    });
  }
  const menuKernel = $("#menu-kernel");
  if (menuKernel) {
    menuKernel.addEventListener("click", function () {
      closeMenus();
      openKernelChooser();
    });
  }
  const menuTrust = $("#menu-trust");
  if (menuTrust) {
    menuTrust.checked = isNotebookTrusted();
    menuTrust.addEventListener("change", function () {
      setNotebookTrusted(!!menuTrust.checked);
    });
  }

  const menuForceSave = $("#menu-force-save");
  if (menuForceSave) {
    menuForceSave.addEventListener("click", function () {
      closeMenus();
      setStatus("Saving…", "run");
      fetch("/api/save", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: path }),
      })
        .then(function (r) {
          setStatus(
            r.ok ? withKernel("Saved") : "Save failed",
            r.ok ? "ok" : "err"
          );
          if (r.ok)
            setTimeout(function () {
              if (sessionReady) setLiveStatus("ok");
            }, 900);
        })
        .catch(function () {
          setStatus("Save failed", "err");
        });
    });
  }

  if (kernelFilter) {
    kernelFilter.addEventListener("input", function () {
      renderKernelList(kernelFilter.value);
    });
  }
  if (kernelList) {
    kernelList.addEventListener("click", function (e) {
      const b = e.target.closest(".kernel-pick");
      if (!b) return;
      const name = b.getAttribute("data-name");
      if (!name) return;
      b.disabled = true;
      const prev = b.innerHTML;
      b.innerHTML =
        '<span class="loading loading-spinner loading-xs"></span><span class="text-xs">Binding…</span>';
      fetch("/api/kernel/bind", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: path, name: name }),
      })
        .then(function (r) {
          if (!r.ok)
            return r.text().then(function (t) {
              throw new Error(t || r.statusText);
            });
          return r.json();
        })
        .then(function (st) {
          applyKernelStatus(st);
          if (kernelDialog) kernelDialog.close();
          setStatus(withKernel("Kernel ready"), "ok");
          setTimeout(function () {
            if (sessionReady) setLiveStatus("ok");
          }, 600);
        })
        .catch(function (err) {
          setStatus(String(err.message || err), "err");
          b.disabled = false;
          b.innerHTML = prev;
        });
    });
  }

  if (path) connect();
})();
