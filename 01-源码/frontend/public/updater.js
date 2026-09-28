(function () {
  var Events = window.wails.Events;

  var els = {
    root:        document.getElementById("root"),
    icon:        document.getElementById("state-icon"),
    title:       document.getElementById("title"),
    subtitle:    document.getElementById("subtitle"),
    notes:       document.getElementById("notes"),
    spinnerText: document.getElementById("spinner-text"),
    bar:         document.getElementById("bar"),
    progressRate:document.getElementById("progress-rate"),
    progressState: document.getElementById("progress-state"),
    error:       document.getElementById("error-text"),
    btnInstall:  document.getElementById("btn-install"),
    btnSkip:     document.getElementById("btn-skip"),
    btnRemind:   document.getElementById("btn-remind"),
    btnCancel:   document.getElementById("btn-cancel"),
    btnRestart:  document.getElementById("btn-restart"),
    btnRetry:    document.getElementById("btn-retry"),
  };

  /*
   * 运行时 API 兼容层（2026-09-27 实测修正）：
   * Wails 注入到页面的是 `window.wails.Events`，**没有**全局 `Events`。
   * 直接用 `Events.On(...)` 会在脚本第一句抛 ReferenceError，导致整个接线死掉、
   * 界面永远停在初始的「正在检查更新…」——这正是自动更新一直"转圈不下载"的根因。
   */
  var Events =
    (window.wails && window.wails.Events) ||
    (window._wails && window._wails.Events) || {
      On: function () {},
      Emit: function () {},
    };

  var ICONS = {
    "checking":    "↻",
    "available":   "⬇",
    "downloading": "⬇",
    "verifying":   "✓",
    "installing":  "⚙",
    "ready":       "✓",
    "up-to-date":  "✓",
    "error":       "!",
  };

  /* 事件总线可能乱序到达，按阶段排名单调推进，防止旧事件把界面拉回去 */
  var RANK = {
    "":            0,
    "checking":    1,
    "available":   2,
    "downloading": 3,
    "verifying":   4,
    "installing":  5,
    "ready":       6,
    "up-to-date":  6,
    "error":       99,
  };
  var STAGE_CN = { "check": "检查阶段", "download": "下载阶段", "verify": "校验阶段", "install": "安装阶段" };
  var rank = 0;
  var errored = false;
  var currentRelease = null;
  var currentVersion = null;
  var skippedVersion = "";

  function setState(name) {
    if (errored && name !== "error") return false;
    if (name === "error") { errored = true; }
    else {
      if ((RANK[name] || 0) < rank) return false;
      rank = RANK[name] || 0;
    }
    els.root.setAttribute("data-state", name);
    els.root.classList.toggle("u--ready",      name === "ready");
    els.root.classList.toggle("u--up-to-date", name === "up-to-date");
    els.root.classList.toggle("u--error",      name === "error");
    if (els.icon) els.icon.textContent = ICONS[name] || "";
    return true;
  }

  function renderMarkdown(src) {
    if (!src) return "";
    function escapeHtml(s) {
      return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
    }
    function inline(s) {
      s = s.replace(/`([^`]+)`/g, function (_, c) { return "<code>" + c + "</code>"; });
      s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
      s = s.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
      s = s.replace(/\[([^\]]+)\]\((https?:[^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
      return s;
    }
    var lines = escapeHtml(src).split(/\r?\n/);
    var out = [];
    var i = 0;
    while (i < lines.length) {
      var L = lines[i];
      if (/^```/.test(L)) {
        var buf = [];
        i++;
        while (i < lines.length && !/^```/.test(lines[i])) { buf.push(lines[i]); i++; }
        out.push("<pre><code>" + buf.join("\n") + "</code></pre>");
        i++; continue;
      }
      var h = /^(#{1,6})\s+(.+)$/.exec(L);
      if (h) {
        var lvl = Math.min(h[1].length, 3);
        out.push("<h" + lvl + ">" + inline(h[2]) + "</h" + lvl + ">");
        i++; continue;
      }
      if (/^\|.*\|$/.test(L) && i + 1 < lines.length && /^\|[\s:|-]+\|$/.test(lines[i+1])) {
        var rows = [L];
        i += 2;
        while (i < lines.length && /^\|.*\|$/.test(lines[i])) { rows.push(lines[i]); i++; }
        var html = "<table>";
        rows.forEach(function (r, idx) {
          var cells = r.replace(/^\||\|$/g, "").split("|").map(function (c) { return inline(c.trim()); });
          var tag = idx === 0 ? "th" : "td";
          html += "<tr><" + tag + ">" + cells.join("</" + tag + "><" + tag + ">") + "</" + tag + "></tr>";
        });
        html += "</table>";
        out.push(html);
        continue;
      }
      var ulRe = /^[-*]\s+(.+)$/;
      var olRe = /^\d+\.\s+(.+)$/;
      if (ulRe.test(L) || olRe.test(L)) {
        var isOL = olRe.test(L);
        var re = isOL ? olRe : ulRe;
        var items = [];
        while (i < lines.length && re.test(lines[i])) {
          items.push("<li>" + inline(re.exec(lines[i])[1]) + "</li>");
          i++;
        }
        out.push("<" + (isOL ? "ol" : "ul") + ">" + items.join("") + "</" + (isOL ? "ol" : "ul") + ">");
        continue;
      }
      if (L.trim() === "") { i++; continue; }
      var para = [L];
      i++;
      while (i < lines.length && lines[i].trim() !== "" && !/^[-*]\s/.test(lines[i]) && !/^\d+\.\s/.test(lines[i]) && !/^\|.*\|$/.test(lines[i]) && !/^#{1,6}\s/.test(lines[i]) && !/^```/.test(lines[i])) {
        para.push(lines[i]); i++;
      }
      out.push("<p>" + inline(para.join(" ")) + "</p>");
    }
    return out.join("\n");
  }

  function fmtBytes(n) {
    if (!n || n <= 0) return "";
    var u = ["B", "KB", "MB", "GB"], k = 1024;
    var i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(k)));
    return (n / Math.pow(k, i)).toFixed(i === 0 ? 0 : 1) + " " + u[i];
  }
  function fmtRate(bps) {
    var s = fmtBytes(bps);
    return s ? s + "/s" : "";
  }

  function renderSubtitle(rel) {
    if (!rel) { els.subtitle.textContent = ""; return; }
    els.subtitle.innerHTML = "";
    if (currentVersion) {
      var from = document.createElement("span"); from.className = "u__ver-from"; from.textContent = "v" + currentVersion;
      var arr  = document.createElement("span"); arr.className  = "u__ver-arrow"; arr.textContent  = "→";
      els.subtitle.appendChild(from);
      els.subtitle.appendChild(arr);
    }
    if (rel.version) {
      var to = document.createElement("span"); to.className = "u__ver-to"; to.textContent = "v" + rel.version;
      els.subtitle.appendChild(to);
    }
    var size = (rel.artifact && rel.artifact.size) || 0;
    if (size > 0) {
      var sz = document.createElement("span"); sz.className = "u__size"; sz.textContent = "· " + fmtBytes(size);
      els.subtitle.appendChild(sz);
    }
  }

  /* === 状态处理 === */
  function onCheckStarted() {
    if (!setState("checking")) return;
    els.title.textContent = "正在检查更新…";
    els.spinnerText.textContent = "正在连接更新服务器…";
  }
  function onUpdateAvailable(rel) {
    currentRelease = rel || null;
    if (!setState("available")) return;
    els.title.textContent = "发现新版本";
    renderSubtitle(rel);
    els.notes.innerHTML = (rel && rel.notes) ? renderMarkdown(rel.notes) : "";
  }
  function onNoUpdate() {
    if (!setState("up-to-date")) return;
    els.title.textContent = "已是最新版本";
    els.subtitle.innerHTML = "";
    if (currentVersion) {
      var v = document.createElement("span");
      v.className = "u__ver-to";
      v.textContent = "v" + currentVersion;
      els.subtitle.appendChild(v);
    }
  }
  function onDownloadStarted(rel) {
    if (!setState("downloading")) return;
    els.title.textContent = "正在下载更新";
    els.progressState.textContent = "正在准备下载…";
    els.progressRate.textContent = "";
    setBar(null, null);
  }
  function setBar(written, total) {
    if (written == null || !total || total <= 0) {
      els.bar.style.width = "40%";
      els.bar.classList.add("u__bar-fill--indet");
      return;
    }
    els.bar.classList.remove("u__bar-fill--indet");
    var pct = Math.min(100, (written / total) * 100);
    els.bar.style.width = pct + "%";
  }
  function onDownloadProgress(p) {
    if (!p) return;
    if (rank !== RANK.downloading) return;
    if (p.total > 0) {
      setBar(p.written, p.total);
      var pct = Math.round((p.written / p.total) * 100);
      els.progressState.textContent = pct + "% · " + fmtBytes(p.written) + " / " + fmtBytes(p.total);
    } else {
      setBar(p.written, 0);
      els.progressState.textContent = "已下载 " + fmtBytes(p.written);
    }
    els.progressRate.textContent = p.rate ? fmtRate(p.rate) : "";
  }
  function onVerifying() {
    if (!setState("verifying")) return;
    els.title.textContent = "正在校验更新";
    els.spinnerText.textContent = "正在校验文件完整性…";
  }
  function onInstalling() {
    if (!setState("installing")) return;
    els.title.textContent = "正在安装更新";
    els.spinnerText.textContent = "正在解压并准备安装…";
  }
  function onUpdateReady() {
    if (!setState("ready")) return;
    els.title.textContent = "更新已就绪";
    if (currentRelease) renderSubtitle(currentRelease);
    if (currentRelease && currentRelease.notes && els.notes) {
      els.notes.innerHTML = renderMarkdown(currentRelease.notes);
    }
  }
  function onError(info) {
    setState("error");
    els.title.textContent = "更新失败";
    var msg = (info && info.message) ? info.message : "发生未知错误。";
    if (info && info.stage) msg = (STAGE_CN[info.stage] || info.stage) + "：" + msg;
    els.error.textContent = msg;
  }

  /* === 事件接线 === */
  Events.On("wails:updater:meta", function (e) {
    var m = e && (e.data != null ? e.data : e);
    if (m && typeof m.currentVersion === "string") currentVersion = m.currentVersion;
    if (m && typeof m.skippedVersion === "string") skippedVersion = m.skippedVersion;
  });
  Events.On("wails:updater:check-started",     function () { onCheckStarted(); });
  Events.On("wails:updater:update-available",  function (e) { onUpdateAvailable(e && (e.data != null ? e.data : e)); });
  Events.On("wails:updater:no-update",         function () { onNoUpdate(); });
  Events.On("wails:updater:download-started",  function (e) { onDownloadStarted(e && (e.data != null ? e.data : e)); });
  Events.On("wails:updater:download-progress", function (e) { onDownloadProgress(e && (e.data != null ? e.data : e)); });
  Events.On("wails:updater:download-complete", function () {});
  Events.On("wails:updater:verifying",         function () { onVerifying(); });
  Events.On("wails:updater:installing",        function () { onInstalling(); });
  Events.On("wails:updater:update-ready",      function (e) { onUpdateReady(); });
  Events.On("wails:updater:error",             function (e) { onError(e && (e.data != null ? e.data : e)); });

  if (els.btnInstall) els.btnInstall.addEventListener("click", function () { Events.Emit("wails:updater:user:install"); });
  if (els.btnSkip)    els.btnSkip.addEventListener   ("click", function () { Events.Emit("wails:updater:user:skip"); });
  if (els.btnRemind)  els.btnRemind.addEventListener ("click", function () { Events.Emit("wails:updater:user:remind"); });
  if (els.btnCancel)  els.btnCancel.addEventListener ("click", function () { Events.Emit("wails:updater:user:cancel"); });
  if (els.btnRestart) els.btnRestart.addEventListener("click", function () { Events.Emit("wails:updater:user:restart"); });
  if (els.btnRetry)   els.btnRetry.addEventListener  ("click", function () { Events.Emit("wails:updater:user:install"); });

  /* 运行时握手：页面就绪后通知框架冲刷积压事件（照抄默认模板，勿删） */
  (function announce() {
    if (window._wails && typeof window._wails.invoke === "function") {
      window._wails.invoke("wails:runtime:ready");
      Events.Emit("wails:updater:window:ready");
    } else {
      setTimeout(announce, 30);
    }
  })();
})();

(function () {
  var got = [];
  var we = (window.wails && window.wails.Events) || (window._wails && window._wails.Events) || null;
  var info = "we=" + (we ? "obj" : "null") + ",On=" + (we && typeof we.On) + ",on=" + (we && typeof we.on);
  function tick() {
    var root = document.getElementById("root");
    document.title =
      "软件更新 | " + info +
      " | got:" + (got.length ? got.join("/") : "-") +
      " | " + (root ? root.getAttribute("data-state") : "?");
  }
  try {
    var fn = we ? (we.On || we.on) : null;
    var names = [
      "wails:updater:meta",
      "wails:updater:update-available",
      "wails:updater:no-update",
      "wails:updater:download-started",
      "wails:updater:update-ready",
      "wails:updater:error",
    ];
    if (fn) {
      names.forEach(function (n) {
        fn.call(we, n, function () {
          var short = n.split(":").pop();
          if (got.indexOf(short) < 0) got.push(short);
          tick();
        });
      });
    }
  } catch (e) {
    info += ",err=" + e.message;
  }
  tick();
  setInterval(tick, 1000);
})();