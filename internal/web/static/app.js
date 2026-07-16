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
  const kernelEl = $("#kernel-pill");

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

  if (path) connect();
  if (kernelEl && cfg.kernel) kernelEl.textContent = cfg.kernel;
})();
