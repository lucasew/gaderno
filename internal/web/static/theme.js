(function () {
  var key = "gaderno-theme";
  var light = "gaderno-light";
  var dark = "gaderno-dark";
  var root = document.documentElement;

  function apply(theme) {
    if (theme !== light && theme !== dark) return;
    root.setAttribute("data-theme", theme);
    try {
      localStorage.setItem(key, theme);
    } catch (e) {}
    var box = document.getElementById("theme-toggle");
    if (box) box.checked = theme === dark;
  }

  window.gadernoSetTheme = apply;

  var box = document.getElementById("theme-toggle");
  if (box) {
    box.checked = root.getAttribute("data-theme") === dark;
    box.addEventListener("change", function () {
      apply(box.checked ? dark : light);
    });
  }
})();
