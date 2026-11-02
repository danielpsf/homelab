// Progressive enhancement for the popover menu: it already opens, closes (Escape, outside tap) and returns focus without this file.
// This only closes it after a link is chosen (the page jumps to a section) and when the layout switches to the wide nav.
(function () {
  var m = document.getElementById("site-menu");
  if (!m || !m.hidePopover) return;
  var hide = function () {
    if (m.matches(":popover-open")) m.hidePopover();
  };
  m.addEventListener("click", function (e) {
    if (e.target.closest("a")) hide();
  });
  window.matchMedia("(min-width:60em)").addEventListener("change", hide);
})();
