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
  const statusEl = $("#status-pill");
  const btnKernel = $("#btn-kernel");

  const collab = createCollabSession();
  let api = null;
  let ws = null;

  function setStatus(text, state) {
    if (!statusEl) return;
    statusEl.textContent = text;
    statusEl.className = "badge badge-xs";
    if (state === "ok") statusEl.classList.add("badge-success");
    else if (state === "run") statusEl.classList.add("badge-info");
    else if (state === "err") statusEl.classList.add("badge-error");
    else statusEl.classList.add("badge-ghost");
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

  // Per-tab hub lifetime fence (SPEC: session identity).
  const sessionStorageKey = "gaderno.session:" + path;
  // True only after hello accepted and hello.ack sent for the current socket.
  let sessionReady = false;

  function connect() {
    if (!path) return;
    sessionReady = false;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const sock = new WebSocket(
      proto + "://" + location.host + "/ws/notebooks/" + path
    );
    ws = sock;
    sock.binaryType = "arraybuffer";
    sock.onopen = function () {
      // Do NOT attach Yjs yet — wait for hello so we never push a previous
      // Y.Doc into a recreated hub before the session fence runs.
      setStatus("connecting", "off");
    };
    sock.onclose = function () {
      if (ws !== sock) return; // superseded
      sessionReady = false;
      setStatus("offline", "off");
      setTimeout(connect, 1500);
    };
    sock.onerror = function () {
      if (ws !== sock) return;
      setStatus("error", "err");
    };
    sock.onmessage = function (ev) {
      if (ws !== sock) return; // ignore events from a previous socket
      if (ev.data instanceof ArrayBuffer) {
        if (!sessionReady) return; // drop CRDT until session fence passes
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
        // "Am I connecting to the same session I was in before?"
        const prev = sessionStorage.getItem(sessionStorageKey);
        const sid = msg.session_id || "";
        if (prev && sid && prev !== sid) {
          // Different hub life: hard reset BEFORE any CRDT traffic.
          sessionStorage.setItem(sessionStorageKey, sid);
          try {
            sock.close();
          } catch (_) {}
          location.reload();
          return;
        }
        if (sid) sessionStorage.setItem(sessionStorageKey, sid);
        // Ack first (server will send sync step1 only after this), then attach.
        sessionReady = true;
        sendJSON({ type: "hello.ack", session_id: sid });
        collab.attachTransport({
          sendBinary: sendBinary,
          sendJSON: sendJSON,
        });
        setStatus("live", "ok");
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
        cell._streamBuf = { stdout: "", stderr: "" };
        const out = $(".out-block", cell);
        if (out) {
          out.hidden = false;
          out.textContent = "";
          out.classList.remove("border-error", "bg-error/10", "text-error");
          out.classList.add("border-info", "text-info");
        }
      } else if (msg.type === "exec.stream") {
        const cell = document.querySelector(
          '.cell-row[data-cell-id="' + msg.cell_id + '"]'
        );
        if (!cell) return;
        const out = $(".out-block", cell);
        if (out) {
          // Server sends full filtered stream so far (not a raw append delta).
          if (!cell._streamBuf) cell._streamBuf = { stdout: "", stderr: "" };
          const name = msg.name === "stderr" ? "stderr" : "stdout";
          cell._streamBuf[name] = msg.text || "";
          out.hidden = false;
          out.classList.add("border-info", "text-info");
          out.textContent =
            (cell._streamBuf.stdout || "") + (cell._streamBuf.stderr || "");
        }
      } else if (msg.type === "exec.result") {
        const cell = document.querySelector(
          '.cell-row[data-cell-id="' + msg.cell_id + '"]'
        );
        if (!cell) return;
        cell._streamBuf = {
          stdout: msg.stdout || "",
          stderr: msg.stderr || "",
        };
        const out = $(".out-block", cell);
        const prompt = $(".prompt-out", cell);
        if (out) {
          out.hidden = false;
          out.classList.remove(
            "border-info",
            "text-info",
            "border-error",
            "bg-error/10",
            "text-error"
          );
          if (msg.status === "error") {
            out.classList.add("border-error", "bg-error/10", "text-error");
          }
          let t = "";
          if (msg.stdout) t += msg.stdout;
          if (msg.stderr) t += msg.stderr;
          if (msg.status === "error")
            t += (msg.ename || "Error") + ": " + (msg.evalue || "");
          if (t) out.textContent = t;
          else if (!out.textContent) out.textContent = msg.status || "ok";
        }
        if (prompt && msg.execution_count != null) {
          prompt.textContent = "Out[" + msg.execution_count + "]:";
        }
        setStatus("live", "ok");
        const runBtn = $(".run", cell);
        if (runBtn) runBtn.disabled = false;
      } else if (msg.type === "error") {
        setStatus(msg.text || "error", "err");
        $all("button.run").forEach(function (b) {
          b.disabled = false;
        });
      } else if (msg.type === "kernel.status") {
        applyKernelStatus(msg.status);
      } else if (msg.type === "kernel.needs_pick") {
        openKernelChooser();
      } else if (msg.type === "chat.message") {
        const log = $("#chat-log");
        if (!log) return;
        const line = document.createElement("div");
        line.className = "py-0.5";
        const who = document.createElement("span");
        who.className = "font-code font-semibold text-primary mr-1";
        who.textContent = msg.from || "?";
        line.appendChild(who);
        line.appendChild(document.createTextNode(msg.text || ""));
        log.appendChild(line);
        log.scrollTop = log.scrollHeight;
      }
    };
  }


  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function buildCellHTML(c) {
    const id = escapeHtml(c.id);
    const typ = c.type === "markdown" ? "markdown" : "code";
    const isCode = typ === "code";
    let html = "";
    html +=
      '<article class="cell-row border-b border-base-300 px-2 py-2 hover:bg-base-200/40" data-cell-id="' +
      id +
      '" data-cell-type="' +
      typ +
      '">';
    html += '<div class="flex flex-col items-end pt-2 gap-0.5 select-none">';
    if (isCode) {
      html +=
        '<span class="font-code text-[0.6875rem] tabular text-primary font-semibold prompt-in">In&nbsp;[ ]:</span>';
      html +=
        '<span class="font-code text-[0.6875rem] tabular text-base-content/50 prompt-out"></span>';
    } else {
      html +=
        '<span class="font-code text-[0.6875rem] text-base-content/50">Md</span>';
    }
    html += '</div><div class="min-w-0">';
    html +=
      '<div class="flex flex-wrap items-center gap-1 min-h-7 mb-1">';
    if (isCode) {
      html +=
        '<button type="button" class="btn btn-primary btn-xs run shrink-0" data-cell-id="' +
        id +
        '">Run</button>';
    }
    html +=
      '<div role="tablist" class="tabs tabs-box tabs-xs shrink-0">' +
      '<button type="button" role="tab" class="tab cell-type-tab' +
      (isCode ? " tab-active" : "") +
      '" data-cell-id="' +
      id +
      '" data-type="code">Code</button>' +
      '<button type="button" role="tab" class="tab cell-type-tab' +
      (!isCode ? " tab-active" : "") +
      '" data-cell-id="' +
      id +
      '" data-type="markdown">Markdown</button></div>';
    if (!isCode) {
      html +=
        '<button type="button" class="btn btn-ghost btn-xs md-toggle shrink-0" data-mode="edit">Preview</button>';
    }
    html += '<span class="flex-1 min-w-1"></span>';
    html +=
      '<details class="dropdown dropdown-end shrink-0">' +
      '<summary class="btn btn-ghost btn-xs btn-square" title="Cell menu" aria-label="Cell menu">···</summary>' +
      '<ul class="menu dropdown-content bg-base-100 rounded-box z-50 w-40 p-1 shadow border border-base-300 text-xs">' +
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
      "</ul></details>";
    html += "</div>";
    if (!isCode) {
      html +=
        '<div class="md-preview text-sm whitespace-pre-wrap" hidden></div>';
    }
    html +=
      '<div class="cm-host" data-gaderno-editor data-cell-id="' +
      id +
      '" data-lang="' +
      (isCode ? "python" : "markdown") +
      '"></div>';
    if (isCode) {
      html +=
        '<div class="out-block mt-1.5 p-2 bg-base-100 border border-base-300 rounded-field font-code text-xs whitespace-pre-wrap break-words" hidden></div>';
    }
    html += "</div></article>";
    return html;
  }

  function applyStructure(cells) {
    const root = document.getElementById("cells");
    if (!root || !Array.isArray(cells)) return;
    // Dedupe by id (first occurrence kept)
    const seen = new Set();
    const unique = [];
    cells.forEach(function (c) {
      if (!c || !c.id || seen.has(c.id)) return;
      seen.add(c.id);
      unique.push(c);
    });
    if (unique.length === 0) {
      root.innerHTML =
        '<p class="text-xs text-base-content/50 p-3">Empty notebook. Use + Code or + Markdown.</p>';
      api = collab.mountEditors(root);
      return;
    }

    // Map current rows. Reuse id+type matches so editors/out-blocks survive.
    // Critical: reorder with insertBefore under #cells — never DocumentFragment
    // (detaching CodeMirror from the document breaks the views).
    $all(":scope > :not(.cell-row)", root).forEach(function (el) {
      el.remove(); // drop empty-state placeholders etc.
    });

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

    // In-document reorder only (insertBefore on an already-attached child).
    unique.forEach(function (c, i) {
      const el = byId.get(c.id);
      if (!el || el.parentNode !== root) return;
      const ref = root.children[i];
      if (ref !== el) {
        root.insertBefore(el, ref || null);
      }
    });

    api = collab.mountEditors(root);
  }


  // Mount collab editors first (empty until Yjs sync fills them)
  api = collab.mountEditors(document.getElementById("cells") || document);

  document.addEventListener("click", function (e) {
    const run = e.target.closest("button.run");
    if (run) {
      const id = run.dataset.cellId;
      if (!id || !ws || ws.readyState !== 1) {
        setStatus("not connected", "err");
        return;
      }
      run.disabled = true;
      setStatus("running", "run");
      const source = flushSource(id);
      const cell = run.closest(".cell-row");
      const out = cell && $(".out-block", cell);
      if (out) {
        out.hidden = false;
        out.classList.remove("border-error", "bg-error/10", "text-error");
        out.classList.add("border-info", "text-info");
        out.textContent = "…";
      }
      sendJSON({ type: "exec.run", cell_id: id, source: source });
      return;
    }

    const mdToggle = e.target.closest("button.md-toggle");
    if (mdToggle) {
      const cell = mdToggle.closest(".cell-row");
      if (!cell || !api) return;
      const id = cell.getAttribute("data-cell-id");
      const preview = $(".md-preview", cell);
      const host = $("[data-gaderno-editor]", cell);
      if (!preview || !host) return;
      const editing = mdToggle.dataset.mode === "edit";
      if (editing) {
        mdToggle.dataset.mode = "preview";
        mdToggle.textContent = "Edit";
        preview.textContent = api.getSource(id);
        preview.hidden = false;
        host.hidden = true;
      } else {
        mdToggle.dataset.mode = "edit";
        mdToggle.textContent = "Preview";
        preview.hidden = true;
        host.hidden = false;
        api.focus(id);
      }
      return;
    }

    const typeTab = e.target.closest("button.cell-type-tab");
    if (typeTab) {
      const id = typeTab.dataset.cellId;
      const typ = typeTab.dataset.type;
      if (!id || !typ) return;
      document.querySelectorAll("details.dropdown[open]").forEach(function (d) {
        d.open = false;
      });
      if (typeTab.classList.contains("tab-active")) return;
      sendJSON({ type: "cell.set_type", cell_id: id, text: typ });
      return;
    }

    const addCode = e.target.closest("#btn-add-code");
    if (addCode) {
      const n = document.querySelectorAll("#cells .cell-row").length;
      sendJSON({ type: "cell.insert", text: "code", index: n });
      return;
    }
    const addMd = e.target.closest("#btn-add-md");
    if (addMd) {
      const n = document.querySelectorAll("#cells .cell-row").length;
      sendJSON({ type: "cell.insert", text: "markdown", index: n });
      return;
    }
    const del = e.target.closest(".cell-del");
    if (del) {
      const id = del.dataset.cellId;
      if (!id) return;
      if (!confirm("Delete this cell?")) return;
      document.querySelectorAll("details.dropdown[open]").forEach(function (d) {
        d.open = false;
      });
      sendJSON({ type: "cell.delete", cell_id: id });
      return;
    }
    const up = e.target.closest(".cell-up");
    if (up) {
      const id = up.dataset.cellId;
      const rows = $all("#cells .cell-row");
      const idx = rows.findIndex(function (r) {
        return r.getAttribute("data-cell-id") === id;
      });
      document.querySelectorAll("details.dropdown[open]").forEach(function (d) {
        d.open = false;
      });
      if (idx > 0) sendJSON({ type: "cell.move", cell_id: id, index: idx - 1 });
      return;
    }
    const down = e.target.closest(".cell-down");
    if (down) {
      const id = down.dataset.cellId;
      const rows = $all("#cells .cell-row");
      const idx = rows.findIndex(function (r) {
        return r.getAttribute("data-cell-id") === id;
      });
      document.querySelectorAll("details.dropdown[open]").forEach(function (d) {
        d.open = false;
      });
      if (idx >= 0 && idx < rows.length - 1)
        sendJSON({ type: "cell.move", cell_id: id, index: idx + 1 });
      return;
    }

    const save = e.target.closest("#btn-save");
    if (save) {
      setStatus("saving", "run");
      fetch("/api/save", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: path }),
      })
        .then(function (r) {
          setStatus(r.ok ? "saved" : "save failed", r.ok ? "ok" : "err");
          if (r.ok) setTimeout(function () { setStatus("live", "ok"); }, 800);
        })
        .catch(function () {
          setStatus("save failed", "err");
        });
    }
  });

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

  // —— Collapsible chat sidebar ——
  const chatRail = document.getElementById("chat-rail");
  const btnChat = document.getElementById("btn-chat");
  const btnChatClose = document.getElementById("btn-chat-close");
  const CHAT_KEY = "gaderno-chat-open";

  function setChatOpen(open) {
    if (!chatRail) return;
    chatRail.dataset.open = open ? "true" : "false";
    chatRail.setAttribute("aria-hidden", open ? "false" : "true");
    if (btnChat) {
      btnChat.setAttribute("aria-expanded", open ? "true" : "false");
      btnChat.classList.toggle("btn-active", open);
    }
    try {
      localStorage.setItem(CHAT_KEY, open ? "1" : "0");
    } catch (_) {}
    if (open) {
      const input = document.getElementById("chat-input");
      if (input) setTimeout(function () { input.focus(); }, 180);
    }
  }

  function toggleChat() {
    if (!chatRail) return;
    setChatOpen(chatRail.dataset.open !== "true");
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

  // —— Kernel chooser ——
  const kernelDialog = document.getElementById("kernel-dialog");
  const kernelList = document.getElementById("kernel-list");
  const kernelFilter = document.getElementById("kernel-filter");
  const kernelLabel = document.getElementById("kernel-label");
  const kernelStateDot = document.getElementById("kernel-state-dot");
  const kernelDialogHint = document.getElementById("kernel-dialog-hint");
  let kernelStatus = {
    needs_kernel: true,
    bound_name: "",
    display_name: "",
    phase: "needs_kernel",
    running: false,
  };
  let autoOpenedChooser = false;
  let kernelCatalog = null; // { groups, kernels }

  function applyKernelStatus(st) {
    if (!st) return;
    kernelStatus = st;
    const needs = st.needs_kernel || !st.bound_name;
    if (kernelLabel) {
      if (needs) {
        kernelLabel.textContent = "Select kernel…";
      } else {
        kernelLabel.textContent = st.display_name || st.bound_name;
      }
    }
    if (kernelStateDot) {
      kernelStateDot.className = "badge badge-xs shrink-0";
      if (needs) {
        kernelStateDot.classList.add("badge-warning");
        kernelStateDot.textContent = "pick";
      } else if (st.running || st.phase === "ready" || st.phase === "busy") {
        kernelStateDot.classList.add("badge-success");
        kernelStateDot.textContent = st.phase === "busy" ? "run" : "on";
      } else if (st.phase === "starting") {
        kernelStateDot.classList.add("badge-info");
        kernelStateDot.textContent = "…";
      } else if (st.phase === "dead") {
        kernelStateDot.classList.add("badge-error");
        kernelStateDot.textContent = "err";
      } else {
        kernelStateDot.classList.add("badge-ghost");
        kernelStateDot.textContent = "idle";
      }
    }
    if (btnKernel) {
      btnKernel.title = needs
        ? "Choose a kernel before running cells"
        : (st.bound_name || "") + " · " + (st.phase || "");
      btnKernel.classList.toggle("btn-warning", !!needs);
      btnKernel.classList.toggle("btn-ghost", !needs);
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
      uv: "Managed by uv (starts with ipykernel on first Run)",
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
        html += "<li class=\"w-full\">";
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
    if (kernelFilter) setTimeout(function () { kernelFilter.focus(); }, 50);
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

  if (btnKernel) {
    btnKernel.addEventListener("click", function () {
      openKernelChooser();
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
          setStatus("kernel ready", "ok");
          setTimeout(function () {
            setStatus("live", "ok");
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
