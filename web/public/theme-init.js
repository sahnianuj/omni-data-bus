// Apply the saved theme before first paint (no flash of the wrong theme). A separate file, not an
// inline script, so the Content-Security-Policy can stay script-src 'self' (OC-033).
;(function () {
  try {
    var pref = localStorage.getItem('oc-theme') || 'system'
    var dark = pref === 'dark' || (pref === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
    if (dark) document.documentElement.classList.add('dark')
  } catch (e) {}
})()
