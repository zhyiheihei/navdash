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
    query: "",
    pins: {},            // url -> true (persisted)
    groups: {},          // url -> custom group name (persisted)
  };

  var el = {
    search: document.getElementById("search"),
    groups: document.getElementById("groups"),
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

  function renderCards() {
    el.groups.textContent = "";
    var q = state.query.trim().toLowerCase();
    var kept = state.entries.filter(function (e) { return !q || matches(e, q); });

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
      var k = state.groups[e.url] || e.host || "other";
      if (!groups[k]) { groups[k] = []; order.push(k); }
      groups[k].push(e);
    });
    order.sort();
    order.forEach(function (k) { list.push({ key: k, entries: groups[k] }); });

    list.forEach(function (g) {
      var section = el.tplGroup.content.firstElementChild.cloneNode(true);
      section.querySelector(".group-title").textContent = g.key;
      section.querySelector(".group-count").textContent = g.entries.length + " 项";
      var grid = section.querySelector(".group-grid");
      g.entries.forEach(function (e) { grid.appendChild(renderCard(e)); });
      el.groups.appendChild(section);
    });

    el.groups.querySelectorAll(".card").forEach(wireCard);
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

    // Card icon: server-side favicon when available, deterministic letter
    // glyph otherwise (see /api/icon). The ?v= cache-buster lets the later
    // refresh pick up a favicon that was fetched after first paint.
    var icon = card.querySelector(".card-icon");
    if (host) {
      icon.decoding = "async";
      icon.alt = highlight;
      icon.src = "/api/icon?host=" + encodeURIComponent(host) + "&v=2";
      // Server always answers (real favicon or letter glyph); if the image
      // itself fails to load, drop it rather than show a broken frame.
      icon.addEventListener("error", function () { icon.hidden = true; });
    } else {
      icon.hidden = true;
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
    return card;
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

  el.themeBtn.addEventListener("click", function () {
    var next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem("nav-theme", next); } catch (err) { /* private mode */ }
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
  Promise.all([getJSON("/api/me"), getJSON("/api/entries"), getJSON("/api/status")])
    .then(function (results) {
      var me = results[0];
      state.authenticated = !!me.authenticated;
      state.username = me.username || "";
      state.entries = results[1].entries || [];
      state.status = results[2].status || {};
      render();
      // The server prefetches favicons in the background after boot; letter
      // glyphs render instantly, then this pass swaps in real favicons once
      // the prefetch has usually settled.
      setTimeout(function () {
        document.querySelectorAll(".card-icon").forEach(function (img) {
          if (!img.src) return;
          var u = new URL(img.src);
          u.searchParams.set("v", "3");
          img.src = u.toString();
        });
      }, 6000);
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