// CPS — Logo mark + theme toggle + small inline helpers.
// Loaded on every page before content scripts. Avoids polluting JS that already exists.

(function () {
  // ── Logo mark SVG (Variant C — ad-hoc mesh of devices) ───────────────────
  // Uses currentColor for ink lines + .signal-dot for the lime centre node.
  const LOGO_MARK = `
<svg viewBox="0 0 80 64" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <g stroke-width="0.7" opacity="0.4" stroke-dasharray="2 2.2">
    <line x1="14" y1="16" x2="40" y2="32"/><line x1="40" y1="32" x2="66" y2="16"/>
    <line x1="66" y1="16" x2="66" y2="48"/><line x1="66" y1="48" x2="14" y2="48"/>
    <line x1="14" y1="16" x2="66" y2="48"/><line x1="14" y1="48" x2="40" y2="32"/>
  </g>
  <g transform="translate(14 16)">
    <circle r="3.4" fill="var(--card,#fbf9f3)"/>
    <line x1="-1.7" y1="-3.4" x2="-1.7" y2="-6.4"/>
    <line x1="1.7"  y1="-3.4" x2="1.7"  y2="-6.4"/>
    <line x1="0"    y1="-3.4" x2="0"    y2="-7"/>
  </g>
  <g transform="translate(66 16)">
    <circle cx="-3.6" cy="-2.6" r="1.7" fill="var(--card,#fbf9f3)"/>
    <circle cx="3.6"  cy="-2.6" r="1.7" fill="var(--card,#fbf9f3)"/>
    <circle cx="-3.6" cy="2.6"  r="1.7" fill="var(--card,#fbf9f3)"/>
    <circle cx="3.6"  cy="2.6"  r="1.7" fill="var(--card,#fbf9f3)"/>
    <rect x="-1.6" y="-0.9" width="3.2" height="1.8" fill="var(--card,#fbf9f3)"/>
  </g>
  <g transform="translate(14 48)">
    <rect x="-2.6" y="-4.4" width="5.2" height="8.8" rx="0.9" fill="var(--card,#fbf9f3)"/>
    <line x1="-1.3" y1="3" x2="1.3" y2="3"/>
  </g>
  <g transform="translate(66 48)">
    <path d="M-4.4 0.8 L-3.6 -1.6 L3.6 -1.6 L4.4 0.8 L4.4 2.6 L-4.4 2.6 Z" fill="var(--card,#fbf9f3)"/>
    <circle cx="-2.6" cy="3" r="1" fill="currentColor" stroke="none"/>
    <circle cx="2.6"  cy="3" r="1" fill="currentColor" stroke="none"/>
  </g>
  <g transform="translate(40 32)">
    <circle r="4.6" class="signal-dot" stroke="currentColor" stroke-width="1.4"/>
    <circle r="1"   fill="currentColor" stroke="none"/>
  </g>
</svg>`.trim();

  // Mount the logo mark into every legacy `.logo-icon` placeholder. Many existing
  // HTML files render `<div class="logo-icon">📡</div>`; we replace innerHTML with
  // the SVG so look-and-feel is consistent without rewriting markup.
  function mountLogoMarks() {
    document.querySelectorAll('.logo-icon, .logo-gem, .logo-mark, [data-logo-mark]').forEach(function (el) {
      // Avoid double-mounting
      if (el.dataset.logoMounted === '1') return;
      el.classList.add('logo-mark');
      el.classList.remove('logo-icon', 'logo-gem');
      el.innerHTML = LOGO_MARK;
      el.dataset.logoMounted = '1';
    });
    // Also: replace `<span>Collaborative</span>PositioningService` with mono-styled lockup
    document.querySelectorAll('.logo-text, .logo-name').forEach(function (el) {
      if (el.dataset.logoTextMounted === '1') return;
      el.dataset.logoTextMounted = '1';
      // Preserve the user-visible long form but visually compact: tag chip + product
      el.innerHTML = '<span class="lock-cps">CPS</span><span class="tag">Collaborative Positioning</span>';
    });
  }

  // ── Theme manager (preserves the old API: _toggleTheme / _applyTheme) ───
  const KEY = 'cps_theme';
  function currentTheme() {
    return localStorage.getItem(KEY) ||
      (window.matchMedia('(prefers-color-scheme:dark)').matches ? 'dark' : 'light');
  }
  function syncToggleButtons(theme) {
    document.querySelectorAll('.theme-toggle').forEach(function (b) {
      b.textContent = theme === 'dark' ? '☀' : '☾';
      b.title = theme === 'dark' ? 'Светлая тема' : 'Тёмная тема';
      b.setAttribute('aria-label', b.title);
    });
  }
  function applyTheme(theme) {
    document.documentElement.setAttribute('data-theme', theme);
    localStorage.setItem(KEY, theme);
    syncToggleButtons(theme);
  }
  function toggleTheme() {
    var cur = document.documentElement.getAttribute('data-theme') || 'light';
    applyTheme(cur === 'dark' ? 'light' : 'dark');
  }
  // Pre-render: set theme attribute ASAP to avoid FOUC
  document.documentElement.setAttribute('data-theme', currentTheme());

  // Expose legacy API
  window._applyTheme = applyTheme;
  window._toggleTheme = toggleTheme;

  // After DOM ready, mount logos and sync buttons
  document.addEventListener('DOMContentLoaded', function () {
    mountLogoMarks();
    syncToggleButtons(currentTheme());
  });

  // For dynamic insertions (e.g. main.js writes auth-buttons after fetch)
  window._cpsRefreshChrome = mountLogoMarks;
})();
