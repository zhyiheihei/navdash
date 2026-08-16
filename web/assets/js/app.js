// navdash frontend: fetch /api/me + /api/entries, render grouped cards,
// live search, theme toggle. No build step, no dependencies.

(function () {
  "use strict";

  var state = {
    authenticated: false,
    username: "",
    entries: [],
    query: "",
  };

  var el = {
    search: document.getElementById("search"),
    groups: document.getElementById("groups"),
    hero: document.getElementById("hero"),
    authSlot: document.getElementById("auth-slot"),
    themeBtn: document.getElementById("theme-btn"),
    tplGroup: document.getElementById("tpl-group"),
    tplCard: document.getElementById("tpl-card"),
  };

  // ------------------------------------------------------------------
  // Rendering

  function groupKey(e) {
    // Group by origin host; private per-host subdomains stay with their host.
    return e.host || "other";
  }

  function groupOrder(g) {
    // Own public domain first, then everything else alphabetically.
    return g;
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

  function renderCards() {
    el.groups.textContent = "";
    var q = state.query.trim().toLowerCase();

    var kept = state.entries.filter(function (e) {
      if (!q) return true;
      return (
        (e.name || "").toLowerCase().indexOf(q) !== -1 ||
        (e.host || "").toLowerCase().indexOf(q) !== -1 ||
        (e.url || "").toLowerCase().indexOf(q) !== -1
      );
    });

    if (kept.length === 0) {
      var empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = q ? "没有匹配的服务。" : "暂无服务条目。";
      el.groups.appendChild(empty);
      return;
    }

    var groups = {};
    var order = [];
    kept.forEach(function (e) {
      var k = groupKey(e);
      if (!groups[k]) {
        groups[k] = [];
        order.push(k);
      }
      groups[k].push(e);
    });
    order.sort(function (a, b) { return groupOrder(a) < groupOrder(b) ? -1 : 1; });

    order.forEach(function (k) {
      var section = el.tplGroup.content.firstElementChild.cloneNode(true);
      section.querySelector(".group-title").textContent = k;
      section.querySelector(".group-count").textContent = groups[k].length + " 项";
      var grid = section.querySelector(".group-grid");
      groups[k].forEach(function (e) { grid.appendChild(renderCard(e)); });
      el.groups.appendChild(section);
    });
  }

  function renderCard(e) {
    var card = el.tplCard.content.firstElementChild.cloneNode(true);
    card.href = e.url;
    card.querySelector(".card-name").textContent = e.name;
    card.querySelector(".card-proto").textContent = e.proto;
    card.querySelector(".card-highlight").textContent = e.highlight;
    card.querySelector(".card-suffix").textContent = e.suffix;
    if (e.access && e.access !== "public") {
      var badge = card.querySelector(".card-badge");
      badge.hidden = false;
      badge.textContent = e.access === "private" ? "private" : e.access;
    }
    return card;
  }

  // ------------------------------------------------------------------
  // Events

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

  // ------------------------------------------------------------------
  // Boot

  function getJSON(url) {
    return fetch(url, { credentials: "same-origin" }).then(function (r) {
      if (!r.ok) throw new Error(url + " " + r.status);
      return r.json();
    });
  }

  Promise.all([getJSON("/api/me"), getJSON("/api/entries")])
    .then(function (results) {
      var me = results[0];
      state.authenticated = !!me.authenticated;
      state.username = me.username || "";
      state.entries = results[1].entries || [];
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
