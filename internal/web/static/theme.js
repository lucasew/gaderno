(function () {
  var key = "gaderno-theme";
  var light = "gaderno-light";
  var dark = "gaderno-dark";
  var root = document.documentElement;
  var box = document.getElementById("theme-toggle");
  function apply(theme) {
    root.setAttribute("data-theme", theme);
    try { localStorage.setItem(key, theme); } catch (e) {}
    if (box) box.checked = theme === dark;
  }
  if (box) {
    box.checked = root.getAttribute("data-theme") === dark;
    box.addEventListener("change", function () {
      apply(box.checked ? dark : light);
    });
  }
})();
