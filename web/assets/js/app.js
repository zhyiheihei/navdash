// navdash frontend: fetch /api/me + /api/entries + /api/status, render
// grouped cards with live health dots, search, pinning and custom grouping.
// No build step, no dependencies; localStorage holds the user's layout.

(function () {
  "use strict";

  // Health latency thresholds (ms), matching gethomepage's indicator.
  var OK = 250;
  var WARN = 1000;

  var state = {
    authenticated: false,
    username: "",
    entries: [],
    status: {},          // url -> {status, latency_ms, code}
    metrics: null,       // {hosts: hostname -> {cpu,memory,disk}, services: url -> widgetData}
    query: "",
    pins: {},            // url -> true (persisted)
    groups: {},          // url -> custom group name (persisted)
    filter: "all",       // 语义筛选：all | 公开 | 私有 | 快捷
  };

  var el = {
    search: document.getElementById("search"),
    groups: document.getElementById("groups"),
    filter: document.getElementById("nav-filter"),
    hero: document.getElementById("hero"),
    authSlot: document.getElementById("auth-slot"),
    themeBtn: document.getElementById("theme-btn"),
    homeBtn: document.getElementById("home-btn"),
    homeDialog: document.getElementById("home-dialog"),
    homeClose: document.getElementById("home-dialog-close"),
    homeCopy: document.getElementById("home-dialog-copy"),
    homeUrl: document.getElementById("home-dialog-url"),
    tplGroup: document.getElementById("tpl-group"),
    tplCard: document.getElementById("tpl-card"),
    moveMenu: document.getElementById("move-menu"),
    moveInput: document.getElementById("move-menu-input"),
  };

  function loadLocal() {
    try {
      var p = JSON.parse(localStorage.getItem("nav-pins") || "[]");
      state.pins = {};
      (Array.isArray(p) ? p : []).forEach(function (u) { state.pins[u] = true; });
      var g = JSON.parse(localStorage.getItem("nav-groups") || "{}");
      state.groups = (g && typeof g === "object") ? g : {};
    } catch (err) { /* private mode: default layout */ }
  }
  function savePins() {
    var list = Object.keys(state.pins).filter(function (u) { return state.pins[u]; });
    try { localStorage.setItem("nav-pins", JSON.stringify(list)); } catch (err) { /* ignore */ }
  }
  function saveGroups() {
    try { localStorage.setItem("nav-groups", JSON.stringify(state.groups)); } catch (err) { /* ignore */ }
  }

  // ------------------------------------------------------------------
  // Rendering

  function healthLevel(url) {
    var st = state.status[url];
    if (!st || st.status === "unknown") return "idle";
    if (st.status === "down") return "down";
    var ms = latencyOf(st);
    if (ms <= OK) return "ok";
    if (ms <= WARN) return "warn";
    return "bad";
  }

  // Sub-millisecond probes serialize latency_ms as 0; guard against the
  // field being absent anyway so we never compute on undefined.
  function latencyOf(st) {
    return typeof st.latency_ms === "number" ? st.latency_ms : 0;
  }

  // Render a Simple Icons brand mark as a data-URI SVG. The fill uses the
  // brand color in light theme, but dark brand colors (e.g. GitHub #181717,
  // Ollama #000000) would vanish on the dark background, so in dark theme we
  // switch to a light neutral that keeps the mark visible. The path data is
  // self-hosted in icons.js (CC0), so no external CDN is involved.
  function brandSVG(brand) {
    var dark = document.documentElement.dataset.theme === "dark";
    var fill = dark && isDarkHex(brand.hex) ? "#e6e6e6" : "#" + brand.hex;
    var svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">' +
      '<path fill="' + fill + '" d="' + brand.path + '"/></svg>';
    return "data:image/svg+xml," + encodeURIComponent(svg);
  }

  // A brand color is "dark" when its relative luminance is low enough that it
  // would be hard to see on the dark card surface.
  function isDarkHex(hex) {
    var r = parseInt(hex.slice(0, 2), 16);
    var g = parseInt(hex.slice(2, 4), 16);
    var b = parseInt(hex.slice(4, 6), 16);
    return (0.2126 * r + 0.7152 * g + 0.0722 * b) < 90;
  }

  function latencyText(url) {
    var st = state.status[url];
    if (!st) return "检测中…";
    switch (st.status) {
      case "up": return latencyOf(st) + "ms";
      case "down": return "不可达";
      default: return "内网";
    }
  }

  function render() {
    renderAuth();
    renderHero();
    renderCards();
  }

  function renderAuth() {
    el.authSlot.textContent = "";
    if (state.authenticated) {
      var chip = document.createElement("span");
      chip.className = "nav-user";
      var avatar = document.createElement("span");
      avatar.className = "nav-user-avatar";
      avatar.textContent = (state.username || "?").slice(0, 1);
      var name = document.createElement("span");
      name.className = "nav-user-name";
      name.textContent = state.username;
      var logout = document.createElement("a");
      logout.className = "nav-user-logout";
      logout.href = "/auth/logout";
      logout.textContent = "登出";
      chip.append(avatar, name, logout);
      el.authSlot.appendChild(chip);
    } else {
      var login = document.createElement("a");
      login.className = "nav-login-btn";
      login.href = "/auth/login";
      login.textContent = "登录";
      el.authSlot.appendChild(login);
    }
  }

  function renderHero() {
    el.hero.hidden = state.authenticated;
  }

  function matches(e, q) {
    return (
      (e.name || "").toLowerCase().indexOf(q) !== -1 ||
      (e.host || "").toLowerCase().indexOf(q) !== -1 ||
      (e.url || "").toLowerCase().indexOf(q) !== -1
    );
  }

  // 语义分组（公开/私有/快捷）在 Nix 求值期写入 e.group；无 group 的条目
  // 回退到物理主机名。筛选按钮据此只显示选中的那一组。
  function semanticGroup(e) {
    return e.group || e.host || "other";
  }

  // 语义分组内的子分类键：功能域（e.category）在 Nix 求值期由
  // serviceCategories 写入，公开/私有/快捷都有；无 category 的条目回退
  // 到物理主机名，让每个大组内部再分小节，避免单组卡片过长。
  function subKey(e) {
    return e.category || e.host || "其他";
  }

  function renderCards() {
    el.groups.textContent = "";
    var q = state.query.trim().toLowerCase();
    var kept = state.entries.filter(function (e) { return !q || matches(e, q); });

    // 语义筛选：非「全部」时只保留对应语义分组的条目（置顶/自定义分组
    // 也一并过滤，点「快捷」就只看快捷卡片）。
    if (state.filter !== "all") {
      kept = kept.filter(function (e) { return semanticGroup(e) === state.filter; });
    }

    if (kept.length === 0) {
      var empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = q ? "没有匹配的服务。" : "暂无服务条目。";
      el.groups.appendChild(empty);
      return;
    }

    // Pinned entries always lead, as their own group.
    var list = [];
    var pinned = kept.filter(function (e) { return state.pins[e.url]; });
    if (pinned.length) {
      list.push({ key: "置顶", entries: pinned });
    }
    kept = kept.filter(function (e) { return !state.pins[e.url]; });

    var groups = {};
    var order = [];
    kept.forEach(function (e) {
      // Semantic bucket (公开/私有/快捷) assigned at Nix eval time; fall
      // back to the physical host for entries without one.
      var k = state.groups[e.url] || e.group || e.host || "other";
      if (!groups[k]) { groups[k] = []; order.push(k); }
      groups[k].push(e);
    });
    // Semantic groups keep a fixed order (公开 → 私有 → 快捷); any other
    // group (custom user groups, host fallbacks) sorts alphabetically after.
    var SEMANTIC = ["公开", "私有", "快捷"];
    order.sort(function (a, b) {
      var ia = SEMANTIC.indexOf(a), ib = SEMANTIC.indexOf(b);
      if (ia !== -1 || ib !== -1) {
        if (ia === -1) return 1;
        if (ib === -1) return -1;
        return ia - ib;
      }
      return a < b ? -1 : a > b ? 1 : 0;
    });
    order.forEach(function (k) { list.push({ key: k, entries: groups[k] }); });

    list.forEach(function (g) {
      var section = el.tplGroup.content.firstElementChild.cloneNode(true);
      section.querySelector(".group-title").textContent = g.key;
      section.querySelector(".group-count").textContent = g.entries.length + " 项";
      var body = section.querySelector(".group-body");

      // 语义分组内部再按子分类分小节；置顶/自定义分组保持单网格。
      if (SEMANTIC.indexOf(g.key) !== -1) {
        var subs = {};
        var subOrder = [];
        g.entries.forEach(function (e) {
          var sk = subKey(e);
          if (!subs[sk]) { subs[sk] = []; subOrder.push(sk); }
          subs[sk].push(e);
        });
        subOrder.sort(function (a, b) { return a < b ? -1 : a > b ? 1 : 0; });
        subOrder.forEach(function (sk) {
          var head = document.createElement("h3");
          head.className = "group-sub";
          head.textContent = sk;
          body.appendChild(head);
          body.appendChild(buildGrid(subs[sk]));
        });
      } else {
        body.appendChild(buildGrid(g.entries));
      }

      el.groups.appendChild(section);
    });

    el.groups.querySelectorAll(".card").forEach(wireCard);
  }

  function buildGrid(entries) {
    var grid = document.createElement("div");
    grid.className = "group-grid";
    entries.forEach(function (e) { grid.appendChild(renderCard(e)); });
    return grid;
  }

  function renderCard(e) {
    var card = el.tplCard.content.firstElementChild.cloneNode(true);
    card.href = e.url;
    card.title = e.url;
    // entry 的原始 url 作为交互状态键：DOM 的 href 属性会规范化成带尾斜杠的绝对 URL，
    // 与 e.url 字符串不一致会导致置顶/分组查找永远落空。
    card.dataset.url = e.url;

    var host = "";
    try { host = new URL(e.url).hostname; } catch (err) { /* keep empty */ }

    // Root-domain entries (e.g. https://zhyi.xin) come from Nix with an
    // empty highlight — the regex splits the whole name into the suffix.
    // Fall back to the hostname so the URL line keeps a bold title instead
    // of an all-dim one.
    var highlight = e.highlight || host || e.url;
    var suffix = e.highlight ? e.suffix : "";

    card.querySelector(".card-name").textContent = e.name;
    card.querySelector(".card-proto").textContent = e.proto;
    card.querySelector(".card-highlight").textContent = highlight;
    card.querySelector(".card-suffix").textContent = suffix;
    card.querySelector(".card-host").textContent = e.host;

    // Card icon, in priority order:
    //   1. e.brand — a Simple Icons slug rendered inline as a theme-aware SVG
    //      (self-hosted, no external CDN, dark brand colors stay visible).
    //   2. e.icon — a mapped PNG self-hosted via /api/icon.
    //   3. the highlight label straight from nasicon.top (unmapped entries).
    // Missing icons are simply hidden — no further fallback machinery.
    var icon = card.querySelector(".card-icon");
    var brand = BRAND_ICONS[e.brand];
    if (brand) {
      icon.hidden = false;
      icon.alt = brand.title;
      icon.src = brandSVG(brand);
    } else {
      var iconName = e.icon || highlight;
      if (iconName) {
        icon.decoding = "async";
        icon.alt = highlight;
        icon.src = e.icon
          ? "/api/icon/" + encodeURIComponent(iconName) + ".png"
          : "https://nasicon.top/icon/" + encodeURIComponent(iconName) + ".png";
        icon.addEventListener("error", function () { icon.hidden = true; });
      } else {
        icon.hidden = true;
      }
    }

    var badge = card.querySelector(".card-badge");
    if (e.access && e.access !== "public") {
      badge.hidden = false;
      badge.textContent = e.access === "private" ? "私有" : e.access;
      badge.dataset.access = e.access;
    }

    var st = state.status[e.url];
    if (st) card.title += " · " +
      ({ up: "可达", down: "不可达", unknown: "内网/不可探测" }[st.status] || st.status) +
      (st.latency_ms ? " · " + st.latency_ms + "ms" : "") +
      (st.code ? " · HTTP " + st.code : "");

    var dot = card.querySelector(".card-dot");
    dot.dataset.level = healthLevel(e.url);

    var lat = card.querySelector(".card-latency");
    lat.textContent = latencyText(e.url);
    if (e.access && e.access !== "public") lat.classList.add("is-unknown");

    var pinBtn = card.querySelector(".card-pin");
    pinBtn.classList.toggle("is-pinned", !!state.pins[e.url]);

    renderMetrics(card, e);

    return card;
  }

  // Live service data on the card body:
  //   - prometheusmetric cards render CPU / memory / disk percentage bars from
  //     state.metrics.hosts[metric_host || host].
  //   - immich/jellyfin/gitea cards render their service-internal widget items
  //     (photos, library counts, repos...) from state.metrics.services[url].
  // Anonymous or not-yet-loaded state leaves the row empty (hidden).
  //
  // Error / no-data state: the backend sets hostMetric.error (PromQL query
  // failed or returned no sample) and widgetData.error (service API call
  // failed). When present we render a single error line instead of silently
  // showing 0% bars or an empty row, so a broken source is visible rather than
  // mistaken for a real zero.
  function renderMetrics(card, e) {
    var row = card.querySelector(".card-metrics");
    row.textContent = "";

    if (!state.metrics) return; // not loaded yet — keep hidden
    if (e.widget === "prometheusmetric") {
      var hostKey = e.metric_host || e.host || "";
      var m = state.metrics.hosts && state.metrics.hosts[hostKey];
      if (!m) return;
      row.hidden = false;
      if (m.error) {
        row.appendChild(errorMetric(m.error));
        return;
      }
      var bars = [
        { k: "cpu", label: "CPU", v: m.cpu },
        { k: "memory", label: "内存", v: m.memory },
        { k: "disk", label: "磁盘", v: m.disk },
      ];
      bars.forEach(function (b) {
        if (typeof b.v !== "number") return;
        var b_ = document.createElement("span");
        b_.className = "metric";
        b_.innerHTML =
          '<span class="metric-track"><span class="metric-fill" data-kind="' + b.k + '"></span></span>' +
          '<span class="metric-label">' + b.label + ' ' + Math.round(b.v) + '%</span>';
        b_.querySelector(".metric-fill").style.width = Math.min(100, Math.max(0, b.v)) + "%";
        row.appendChild(b_);
      });
      return;
    }

    // Service widget data (immich / jellyfin / gitea ...)
    if (!e.widget) return;
    var svc = state.metrics.services && state.metrics.services[e.url];
    if (!svc) return;
    if (svc.error) {
      row.hidden = false;
      row.appendChild(errorMetric(svc.error));
      return;
    }
    if (!svc.items || !svc.items.length) return;
    row.hidden = false;
    svc.items.forEach(function (it) {
      var span = document.createElement("span");
      span.className = "svc-metric";
      span.innerHTML = '<span class="svc-metric-value"></span><span class="svc-metric-label"></span>';
      span.querySelector(".svc-metric-value").textContent = it.value;
      span.querySelector(".svc-metric-label").textContent = it.label;
      row.appendChild(span);
    });
  }

  // Build a compact error line for a failed / no-data metric source. The
  // backend error string is truncated to keep the card tidy; the full message
  // rides along as a title tooltip.
  function errorMetric(msg) {
    var span = document.createElement("span");
    span.className = "metric-error";
    var text = (msg || "数据不可用").trim();
    if (text.length > 48) text = text.slice(0, 48) + "…";
    span.textContent = text;
    span.title = msg || "";
    return span;
  }

  function wireCard(card) {
    card.querySelector(".card-pin").addEventListener("click", function (ev) {
      ev.preventDefault();
      ev.stopPropagation();
      var url = card.dataset.url || card.href;
      if (state.pins[url]) { delete state.pins[url]; }
      else { state.pins[url] = true; }
      savePins();
      renderCards();
    });
    card.querySelector(".card-menu").addEventListener("click", function (ev) {
      ev.preventDefault();
      ev.stopPropagation();
      openMoveMenu(card);
    });
  }

  // ------------------------------------------------------------------
  // Group-move popover

  function openMoveMenu(card) {
    var url = card.dataset.url || card.href;
    el.moveInput.value = state.groups[url] || "";
    el.moveMenu.hidden = false;
    var r = card.getBoundingClientRect();
    el.moveMenu.style.left = Math.min(r.left, window.innerWidth - 220) + "px";
    el.moveMenu.style.top = (r.bottom + 6) + "px";
    el.moveMenu.dataset.url = url;
    el.moveInput.focus();
    el.moveInput.select();
  }

  function closeMoveMenu() {
    el.moveMenu.hidden = true;
    el.moveMenu.dataset.url = "";
  }

  document.addEventListener("click", function (ev) {
    if (!el.moveMenu.hidden && !el.moveMenu.contains(ev.target)) closeMoveMenu();
  });

  el.moveMenu.addEventListener("submit", function (ev) { ev.preventDefault(); });
  el.moveInput.addEventListener("keydown", function (ev) {
    if (ev.key !== "Enter") return;
    var url = el.moveMenu.dataset.url;
    if (!url) return;
    var name = el.moveInput.value.trim();
    if (name) { state.groups[url] = name; }
    else { delete state.groups[url]; }
    saveGroups();
    closeMoveMenu();
    renderCards();
  });

  // ------------------------------------------------------------------
  // Browser-home guidance dialog

  el.homeBtn.addEventListener("click", function () {
    if (typeof el.homeDialog.showModal === "function") {
      el.homeDialog.showModal();
    } else {
      el.homeDialog.setAttribute("open", "");
    }
  });
  el.homeClose.addEventListener("click", function () { el.homeDialog.close(); });
  el.homeDialog.addEventListener("click", function (ev) {
    if (ev.target === el.homeDialog) el.homeDialog.close();
  });
  el.homeCopy.addEventListener("click", function () {
    var url = el.homeUrl.textContent;
    function done() {
      el.homeCopy.textContent = "已复制";
      setTimeout(function () { el.homeCopy.textContent = "复制地址"; }, 1600);
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).then(done, done);
    } else {
      var ta = document.createElement("textarea");
      ta.value = url;
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand("copy"); done(); } catch (err) { /* ignore */ }
      ta.remove();
    }
  });

  // ------------------------------------------------------------------
  // Search / theme / keyboard

  var debounceTimer = null;
  el.search.addEventListener("input", function () {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(function () {
      state.query = el.search.value;
      renderCards();
    }, 90);
  });

  // 分组筛选：点选「全部/公开/私有/快捷」只显示对应语义分组，并高亮当前项。
  el.filter.addEventListener("click", function (ev) {
    var btn = ev.target.closest(".nav-filter-btn");
    if (!btn) return;
    state.filter = btn.dataset.filter || "all";
    el.filter.querySelectorAll(".nav-filter-btn").forEach(function (b) {
      var on = b === btn;
      b.classList.toggle("is-active", on);
      b.setAttribute("aria-selected", on ? "true" : "false");
    });
    renderCards();
  });

  el.themeBtn.addEventListener("click", function () {
    var next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem("nav-theme", next); } catch (err) { /* private mode */ }
    // Brand icons are rendered as data-URI SVGs whose fill depends on the
    // theme; re-render so dark brand colors switch to a visible neutral.
    renderCards();
  });

  // "/" focuses search (unless already typing in it).
  document.addEventListener("keydown", function (ev) {
    if (ev.key === "/" && document.activeElement !== el.search) {
      ev.preventDefault();
      el.search.focus();
    }
  });

  // Status comes from the server and may update while the page is open;
  // refresh the dots from time to time without a full reload.
  setInterval(function () {
    fetch("/api/status", { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        state.status = d.status || {};
        if (el.groups.childElementCount) renderCards();
      })
      .catch(function () { /* keep last known */ });
    // Live metric/widget data refreshes on the same cadence. Only re-render
    // when metrics actually changed so the page isn't rebuilt on every tick.
    fetch("/api/metrics", { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (state.metrics && JSON.stringify(state.metrics) === JSON.stringify(d)) return;
        state.metrics = d || { hosts: {}, services: {} };
        if (el.groups.childElementCount) renderCards();
      })
      .catch(function () { /* keep last known */ });
  }, 30000);

  // ------------------------------------------------------------------
  // Boot

  function getJSON(url) {
    return fetch(url, { credentials: "same-origin" }).then(function (r) {
      if (!r.ok) throw new Error(url + " " + r.status);
      return r.json();
    });
  }

  loadLocal();
  Promise.all([getJSON("/api/me"), getJSON("/api/entries"), getJSON("/api/status"), getJSON("/api/metrics")])
    .then(function (results) {
      var me = results[0];
      state.authenticated = !!me.authenticated;
      state.username = me.username || "";
      state.entries = results[1].entries || [];
      state.status = results[2].status || {};
      state.metrics = results[3] || { hosts: {}, services: {} };
      render();
    })
    .catch(function (err) {
      console.error(err);
      el.groups.textContent = "";
      var empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "加载失败，请刷新重试。";
      el.groups.appendChild(empty);
    });
})();