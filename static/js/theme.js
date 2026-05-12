/* ══════════════════════════════════════════
   THEME MANAGER
   Подключить в <head> как первый скрипт,
   чтобы избежать мигания при загрузке.
══════════════════════════════════════════ */
(function () {
    const STORAGE_KEY = 'cps_theme';
    const preferred = localStorage.getItem(STORAGE_KEY) ||
        (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');

    function apply(theme) {
        document.documentElement.setAttribute('data-theme', theme);
        localStorage.setItem(STORAGE_KEY, theme);
    }

    apply(preferred);

    /* Публичный API */
    window.ThemeManager = {
        toggle() {
            const cur = document.documentElement.getAttribute('data-theme') || 'light';
            const next = cur === 'dark' ? 'light' : 'dark';
            apply(next);
            document.querySelectorAll('.theme-toggle').forEach(btn => {
                btn.setAttribute('data-icon', next === 'dark' ? 'sun' : 'moon');
                btn.setAttribute('data-icon-size','16');
                btn.textContent = '';
                btn.title = next === 'dark' ? 'Светлая тема' : 'Тёмная тема';
                if (window.applyIcons) window.applyIcons(btn.parentNode);
            });
        },
        current() {
            return document.documentElement.getAttribute('data-theme') || 'light';
        },
        /** Вызвать после рендера DOM, чтобы установить правильный эмодзи на кнопке */
        syncButtons() {
            const isDark = ThemeManager.current() === 'dark';
            document.querySelectorAll('.theme-toggle').forEach(btn => {
                btn.setAttribute('data-icon', isDark ? 'sun' : 'moon');
                btn.setAttribute('data-icon-size','16');
                btn.textContent = '';
                btn.title = isDark ? 'Светлая тема' : 'Тёмная тема';
                if (window.applyIcons) window.applyIcons(btn.parentNode);
            });
        }
    };
})();