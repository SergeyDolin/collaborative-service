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
      b.setAttribute('data-icon', theme === 'dark' ? 'sun' : 'moon');
      b.setAttribute('data-icon-size','16');
      b.textContent = '';
      b.title = theme === 'dark' ? 'Светлая тема' : 'Тёмная тема';
      b.setAttribute('aria-label', b.title);
      if (window.applyIcons) window.applyIcons(b.parentNode || document);
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

  // ── Topographic background (isolines + satellites) ───────────────────────
  // Injected globally so every page replaces the legacy grid backdrop.
  // Resolve the URL relative to this script so it works under any base path.
  (function loadTopoBg() {
    if (document.querySelector('script[data-topo-bg]')) return;
    var cur = document.currentScript;
    if (!cur) {
      var all = document.getElementsByTagName('script');
      for (var i = 0; i < all.length; i++) {
        if (/chrome\.js/.test(all[i].src)) { cur = all[i]; break; }
      }
    }
    var base = cur ? cur.src.replace(/chrome\.js(\?.*)?$/, '') : '/js/';
    var s = document.createElement('script');
    s.src = base + 'topo-bg.js';
    s.setAttribute('data-topo-bg', '1');
    (document.head || document.documentElement).appendChild(s);
  })();

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

  // ── Global header avatar (appears on every page when authenticated) ──────
  // Looks for `.user-menu` or `.user-info` and prepends a clickable avatar
  // chip that always navigates to /profile. Hidden on the profile page itself.
  function mountAvatarLink() {
    const isAuth   = !!localStorage.getItem('token');
    const onProfile = location.pathname.replace(/\/$/, '') === '/profile';
    if (!isAuth) return;
    const slots = document.querySelectorAll('.user-menu, .user-info');
    slots.forEach(function (slot) {
    if (slot.querySelector('.cps-avatar-link, .user-avatar')) return;
      // Skip if it's inside a hero/profile context (avoid double avatar in profile hero)
      if (slot.closest('.profile-hero')) return;
      const a = document.createElement('a');
      a.href = '/profile';
      a.className = 'cps-avatar-link';
      a.title = onProfile ? 'Личный кабинет' : 'Перейти в личный кабинет';
      a.setAttribute('aria-label', a.title);
      // Build avatar with initial letter (if login known) or user icon fallback
      const login = localStorage.getItem('userLogin') || '';
      const avatarUrl = localStorage.getItem('userAvatar')
                     || localStorage.getItem('_av_cache')
                     || '';
      if (avatarUrl) {
        a.innerHTML = '<img src="' + avatarUrl + '" alt=""/>';
      } else if (login) {
        a.innerHTML = '<span class="cps-avatar-initial">' + login.charAt(0).toUpperCase() + '</span>';
      } else {
        a.innerHTML = '<span data-icon="user" data-icon-size="18"></span>';
      }
      // Prepend so avatar sits before name/logout
      slot.insertBefore(a, slot.firstChild);

      // If no cached avatar, fetch and inject when it arrives
      if (!avatarUrl && localStorage.getItem('token')) {
        fetch('/api/profile/data', { headers: { 'Authorization': 'Bearer ' + localStorage.getItem('token') } })
          .then(r => r.ok ? r.json() : null)
          .then(d => {
            if (d && d.avatar) {
              localStorage.setItem('_av_login', login);
              localStorage.setItem('_av_cache', d.avatar);
              a.innerHTML = '<img src="' + d.avatar + '" alt=""/>';
            }
          })
          .catch(() => {});
      }
    });
    if (window.applyIcons) window.applyIcons(document);
  }

  document.addEventListener('DOMContentLoaded', mountAvatarLink);
  // Re-mount if main.js or others repaint the user menu
  window._cpsRefreshAvatar = mountAvatarLink;

  // ── Session / token-expiry guard ────────────────────────────────────────
  // Тихий перелогин потребовал бы хранить пароль или refresh-токен в браузере —
  // и то и другое небезопасно. Поэтому:
  //  1) заранее вычисляем момент истечения JWT и чистим сессию в этот момент;
  //  2) любой ответ 401 от /api/* трактуем как истечение сессии — чистим
  //     локальные данные и уводим на /login с сохранением исходного URL;
  //  3) на публичных страницах ничего не делаем.
  const AUTH_PATHS = new Set(['/login', '/register', '/', '/index', '/terms']);
  function normPath() { return location.pathname.replace(/\/+$/, '') || '/'; }
  function isAuthPage() { return AUTH_PATHS.has(normPath()); }

  function decodeJwtExpMs(token) {
    try {
      const p = token.split('.')[1]; if (!p) return 0;
      const json = atob(p.replace(/-/g, '+').replace(/_/g, '/'));
      const c = JSON.parse(json);
      return (c && typeof c.exp === 'number') ? c.exp * 1000 : 0;
    } catch (_) { return 0; }
  }
  function clearSession() {
    localStorage.removeItem('token');
    localStorage.removeItem('userLogin');
    localStorage.removeItem('_av_cache');
    localStorage.removeItem('_av_login');
    localStorage.removeItem('userAvatar');
  }
  let _redirecting = false;
  function redirectToLogin() {
    if (_redirecting || isAuthPage()) return;
    _redirecting = true;
    const back = encodeURIComponent(location.pathname + location.search);
    location.replace('/login?expired=1&back=' + back);
  }
  function endSession() { clearSession(); redirectToLogin(); }

  // Проактивная проверка при загрузке + таймер до exp.
  (function scheduleExpiry() {
    const t = localStorage.getItem('token');
    if (!t) return;
    const expMs = decodeJwtExpMs(t);
    if (!expMs) return;
    const now = Date.now();
    if (expMs <= now) { endSession(); return; }
    // setTimeout ограничен ~24.8 дн; для токенов такого не бывает, но подстрахуемся.
    const delay = Math.min(expMs - now, 2147483000);
    setTimeout(endSession, delay);
  })();

  // Ссылки на API, которые НЕ должны триггерить выход при 401
  // (это как раз попытка входа/регистрации).
  function isApiUrl(u) {
    if (!u) return false;
    if (u.indexOf('/api/') === 0) return true;
    try { return new URL(u, location.origin).pathname.indexOf('/api/') === 0; }
    catch (_) { return false; }
  }
  function isAuthEndpoint(u) {
    return /\/api\/(login|register)(\?|$)/.test(u);
  }

  // Перехват fetch
  const _origFetch = window.fetch ? window.fetch.bind(window) : null;
  if (_origFetch) {
    window.fetch = function (input, init) {
      const url = typeof input === 'string' ? input : (input && input.url) || '';
      return _origFetch(input, init).then(function (res) {
        if (res && res.status === 401 && isApiUrl(url) && !isAuthEndpoint(url)) {
          endSession();
        }
        return res;
      });
    };
  }

  // Перехват XMLHttpRequest (используется, например, при загрузке измерений)
  const _XHRopen = XMLHttpRequest.prototype.open;
  const _XHRsend = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function (method, url) {
    this.__cps_url = url;
    return _XHRopen.apply(this, arguments);
  };
  XMLHttpRequest.prototype.send = function () {
    const self = this;
    this.addEventListener('load', function () {
      if (self.status === 401 && isApiUrl(self.__cps_url) && !isAuthEndpoint(self.__cps_url)) {
        endSession();
      }
    });
    return _XHRsend.apply(this, arguments);
  };

  // Экспорт для страниц входа
  window._cpsClearSession = clearSession;
})();
