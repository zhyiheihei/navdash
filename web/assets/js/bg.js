// navdash hero background — own implementation of the same visual recipe
// the DeepSeek Harness site uses: a softly blurred particle-network layer
// plus a sharp one, fading out toward the bottom via a mask.
(function () {
  "use strict";

  var main = document.getElementById("bg-network");
  var glow = document.getElementById("bg-network-glow");
  if (!main || !glow) return;
  if (window.innerWidth < 768) return; // the reference site hides it on small screens too
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

  var gMain = main.getContext("2d");
  var gGlow = glow.getContext("2d");

  var brand = "#4d6bfe";
  function readBrand() {
    var v = getComputedStyle(document.documentElement)
      .getPropertyValue("--ds-color-brand").trim();
    if (v) brand = v;
  }
  readBrand();
  // Theme toggles swap the brand color; re-read it on change.
  new MutationObserver(readBrand).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });

  function fit(cv) {
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    var w = cv.clientWidth, h = cv.clientHeight;
    cv.width = Math.round(w * dpr);
    cv.height = Math.round(h * dpr);
    cv.getContext("2d").setTransform(dpr, 0, 0, dpr, 0, 0);
    return { w: w, h: h };
  }

  var size = { w: 0, h: 0 };
  var N = 0;
  var pts = [];

  function reset() {
    size = fit(main); fit(glow);
    N = Math.round(Math.min(70, size.w / 18));
    pts = [];
    for (var i = 0; i < N; i++) {
      pts.push({
        x: Math.random() * size.w,
        y: Math.random() * size.h,
        vx: (Math.random() - 0.5) * 0.5,
        vy: (Math.random() - 0.5) * 0.5,
        r: Math.random() * 1.6 + 0.6,
      });
    }
  }

  var mouse = { x: -9999, y: -9999 };
  window.addEventListener("mousemove", function (e) {
    mouse.x = e.clientX;
    mouse.y = e.clientY;
  });
  window.addEventListener("mouseout", function () {
    mouse.x = -9999; mouse.y = -9999;
  });

  var LINK = 110;     // connect particles closer than this
  var MOUSE_R = 130;  // mouse push radius
  var prev = 0;
  var rafId = 0;

  function frame(now) {
    if (!rafId) return;
    var dt = Math.min(0.05, (now - prev) / 1000 || 0.016);
    prev = now;

    gMain.clearRect(0, 0, size.w, size.h);
    gGlow.clearRect(0, 0, size.w, size.h);

    // drift
    for (var i = 0; i < N; i++) {
      var p = pts[i];
      p.x += p.vx * dt * 60;
      p.y += p.vy * dt * 60;
      if (p.x < -20) p.x = size.w + 20; else if (p.x > size.w + 20) p.x = -20;
      if (p.y < -20) p.y = size.h + 20; else if (p.y > size.h + 20) p.y = -20;
      // gentle mouse repulsion
      var dx = p.x - mouse.x, dy = p.y - mouse.y;
      var d2 = dx * dx + dy * dy;
      if (d2 < MOUSE_R * MOUSE_R && d2 > 0.01) {
        var d = Math.sqrt(d2), f = (MOUSE_R - d) / MOUSE_R;
        p.x += (dx / d) * f * 2.2;
        p.y += (dy / d) * f * 2.2;
      }
    }

    // links
    for (var i = 0; i < N; i++) {
      for (var j = i + 1; j < N; j++) {
        var a = pts[i], b = pts[j];
        var dx = a.x - b.x, dy = a.y - b.y;
        var d2 = dx * dx + dy * dy;
        if (d2 < LINK * LINK) {
          var alpha = (1 - Math.sqrt(d2) / LINK) * 0.22;
          gMain.strokeStyle = brand;
          gMain.globalAlpha = alpha;
          gMain.lineWidth = 1;
          gMain.beginPath();
          gMain.moveTo(a.x, a.y);
          gMain.lineTo(b.x, b.y);
          gMain.stroke();
        }
      }
    }
    gMain.globalAlpha = 1;

    // dots
    for (var i = 0; i < N; i++) {
      var p = pts[i];
      gMain.fillStyle = brand;
      gMain.globalAlpha = 0.55;
      gMain.beginPath();
      gMain.arc(p.x, p.y, p.r, 0, 6.2832);
      gMain.fill();
    }
    gMain.globalAlpha = 1;

    // blurred layer: same geometry, heavy shadow, played back at low opacity
    gGlow.drawImage(main, 0, 0, size.w, size.h);
    gGlow.globalCompositeOperation = "source-atop";
    gGlow.globalAlpha = 0.12;
    gGlow.shadowColor = brand;
    gGlow.shadowBlur = 26;
    gGlow.drawImage(main, 0, 0, size.w, size.h);
    gGlow.globalAlpha = 1;
    gGlow.globalCompositeOperation = "source-over";

    rafId = requestAnimationFrame(frame);
  }

  // The hero starts hidden in the HTML and is revealed by app.js once the
  // session is known (anonymous) — and stays hidden for logged-in sessions,
  // where the loop must not run at all. Tie the loop to hero visibility.
  var hero = document.getElementById("hero");

  function start() {
    if (rafId) return;
    reset();
    prev = 0;
    rafId = requestAnimationFrame(frame);
  }

  function stop() {
    if (rafId) cancelAnimationFrame(rafId);
    rafId = 0;
  }

  if (hero) {
    if (!hero.hidden) start();
    new MutationObserver(function () {
      if (hero.hidden) stop();
      else start();
    }).observe(hero, { attributes: true, attributeFilter: ["hidden"] });
  }

  window.addEventListener("resize", function () {
    if (rafId) reset();
  });
})();