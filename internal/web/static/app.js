import { mountEditors } from "./editor.js";

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

  // Debounce timers per cell
  const pending = new Map();
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

  function flushSource(cellId) {
    if (!api) return "";
    const source = api.getSource(cellId);
    sendJSON({ type: "cell.set_source", cell_id: cellId, source: source });
    const t = pending.get(cellId);
    if (t) {
      clearTimeout(t);
      pending.delete(cellId);
    }
    return source;
  }

  function scheduleSource(cellId, source) {
    setStatus("editing…", "run");
    const prev = pending.get(cellId);
    if (prev) clearTimeout(prev);
    pending.set(
      cellId,
      setTimeout(function () {
        pending.delete(cellId);
        if (sendJSON({ type: "cell.set_source", cell_id: cellId, source: source })) {
          // ack updates status
        }
      }, 200)
    );
  }

  function connect() {
    if (!path) return;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(proto + "://" + location.host + "/ws/notebooks/" + path);
    ws.binaryType = "arraybuffer";
    ws.onopen = function () {
      setStatus("live", "ok");
    };
    ws.onclose = function () {
      setStatus("offline", "off");
      setTimeout(connect, 1500);
    };
    ws.onerror = function () {
      setStatus("error", "err");
    };
    ws.onmessage = function (ev) {
      if (typeof ev.data !== "string") return;
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (_) {
        return;
      }
      if (msg.type === "cell.source_ack") {
        setStatus("live", "ok");
      } else if (msg.type === "exec.result") {
        const cell = document.querySelector(
          '.cell-row[data-cell-id="' + msg.cell_id + '"]'
        );
        if (!cell) return;
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
          out.textContent = t || msg.status || "ok";
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

  // Mount editors
  api = mountEditors(document.getElementById("cells") || document, {
    onChange: scheduleSource,
  });

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
      // flush source on run so kernel sees latest
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
        // switch to preview
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

    const save = e.target.closest("#btn-save");
    if (save) {
      // flush all editors first
      $all("[data-gaderno-editor]").forEach(function (host) {
        flushSource(host.getAttribute("data-cell-id"));
      });
      setStatus("saving", "run");
      setTimeout(function () {
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
      }, 50);
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


  // —— Collapsible chat sidebar (Docs-style) ——
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
  if (btnChatClose) btnChatClose.addEventListener("click", function () { setChatOpen(false); });

  // Restore preference (default closed — more room for editors)
  try {
    setChatOpen(localStorage.getItem(CHAT_KEY) === "1");
  } catch (_) {
    setChatOpen(false);
  }


  // —— Kernel chooser ——
  const kernelDialog = document.getElementById("kernel-dialog");
  const kernelList = document.getElementById("kernel-list");
  let kernelStatus = { needs_kernel: true, bound_name: "", display_name: "", phase: "needs_kernel" };

  function applyKernelStatus(st) {
    if (!st) return;
    kernelStatus = st;
    if (btnKernel) {
      if (st.needs_kernel || !st.bound_name) {
        btnKernel.textContent = "Select kernel…";
        btnKernel.classList.add("text-warning");
      } else {
        const label = st.display_name || st.bound_name;
        const run = st.running ? " · on" : "";
        btnKernel.textContent = label + run;
        btnKernel.classList.remove("text-warning");
        btnKernel.title = st.bound_name + " (" + st.phase + ")";
      }
    }
    if (st.needs_kernel && kernelDialog && !kernelDialog.open) {
      // auto-open chooser once when needed
      openKernelChooser();
    }
  }

  async function openKernelChooser() {
    if (!kernelDialog || !kernelList) return;
    kernelList.innerHTML = '<p class="text-base-content/50 px-1 py-2">Loading…</p>';
    kernelDialog.showModal();
    try {
      const r = await fetch("/api/kernels");
      const data = await r.json();
      const groups = data.groups || {};
      const order = ["jupyter", "uv"];
      const titles = { jupyter: "Jupyter", uv: "uv" };
      let html = "";
      let any = false;
      order.forEach(function (g) {
        const items = groups[g] || [];
        if (!items.length) return;
        any = true;
        html += '<div class="mb-2">';
        html += '<div class="text-[0.625rem] uppercase tracking-wide font-semibold text-base-content/45 px-1 py-1">' + titles[g] + "</div>";
        items.forEach(function (k) {
          const sel = kernelStatus.bound_name === k.name ? " border-primary bg-primary/10" : "";
          html +=
            '<button type="button" class="kernel-pick btn btn-ghost btn-sm w-full justify-start font-normal h-auto min-h-0 py-1.5 px-2 mb-0.5 border border-transparent' +
            sel +
            '" data-name="' +
            k.name.replace(/"/g, "&quot;") +
            '">';
          html += '<span class="truncate text-left"><span class="font-medium">' + escapeHtml(k.display_name || k.name) + "</span>";
          html += '<span class="block text-[0.625rem] text-base-content/45 font-code">' + escapeHtml(k.name) + "</span></span></button>";
        });
        html += "</div>";
      });
      if (!any) {
        html = '<p class="text-base-content/50 px-1 py-2">No kernels found. Install a Jupyter kernelspec or <span class="font-code">uv</span>.</p>';
      }
      kernelList.innerHTML = html;
    } catch (e) {
      kernelList.innerHTML = '<p class="text-error px-1 py-2">Failed to load kernels</p>';
    }
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  if (btnKernel) {
    btnKernel.addEventListener("click", function () {
      openKernelChooser();
    });
  }
  if (kernelList) {
    kernelList.addEventListener("click", function (e) {
      const b = e.target.closest(".kernel-pick");
      if (!b) return;
      const name = b.getAttribute("data-name");
      if (!name) return;
      b.disabled = true;
      fetch("/api/kernel/bind", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: path, name: name }),
      })
        .then(function (r) {
          if (!r.ok) return r.text().then(function (t) { throw new Error(t || r.statusText); });
          return r.json();
        })
        .then(function (st) {
          applyKernelStatus(st);
          if (kernelDialog) kernelDialog.close();
          setStatus("kernel bound", "ok");
          setTimeout(function () { setStatus("live", "ok"); }, 600);
        })
        .catch(function (err) {
          setStatus(String(err.message || err), "err");
          b.disabled = false;
        });
    });
  }

  // patch message handler additions via monkey - insert into onmessage by replacing
  if (path) connect();
})();
