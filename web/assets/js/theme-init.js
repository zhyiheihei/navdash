// Apply the remembered theme before first paint to avoid a flash.
// Kept as a blocking <script> in <head> (no defer) so it runs exactly like
// the inline snippet it replaces — the CSP allows same-origin scripts only.
(function () {
  var t = localStorage.getItem("nav-theme");
  if (!t) t = matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  document.documentElement.dataset.theme = t;
})();
