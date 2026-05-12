/* ════════════════════════════════════════════
   ICONS — line-art SVG icons (replaces emoji)
   Usage: ICONS.gnss_receiver({size:24})
   All return SVG string. Use `currentColor` for stroke.
   ════════════════════════════════════════════ */
(function () {
    const SW = 1.6;          // default stroke width
    const VB = 'viewBox="0 0 24 24"';
    const COMMON = `fill="none" stroke="currentColor" stroke-width="${SW}" stroke-linecap="round" stroke-linejoin="round"`;

    function wrap(size, body, extra = '') {
        const s = size || 24;
        return `<svg width="${s}" height="${s}" ${VB} ${COMMON} ${extra}>${body}</svg>`;
    }

    const ICONS = {
        // ── DEVICE TYPES ──
        gnss_receiver: (o = {}) => wrap(o.size, `
            <path d="M4 10 L8 4 L16 4 L20 10 Z"/>
            <line x1="12" y1="10" x2="12" y2="20"/>
            <line x1="8" y1="20" x2="16" y2="20"/>`),
        smartphone: (o = {}) => wrap(o.size, `
            <rect x="7" y="3" width="10" height="18" rx="2"/>
            <line x1="10" y1="18" x2="14" y2="18"/>`),
        tablet: (o = {}) => wrap(o.size, `
            <rect x="4" y="3" width="16" height="18" rx="2"/>
            <line x1="10" y1="18" x2="14" y2="18"/>`),
        other: (o = {}) => wrap(o.size, `
            <path d="M14.7 6.3a3 3 0 0 1-3.5 4.7L4 18.5 5.5 20l7.5-7.2a3 3 0 0 1 4.7-3.5l-2 2 1.4 1.4 2-2A3 3 0 0 1 14.7 6.3z"/>`),

        // ── MOUNT TYPES ──
        car: (o = {}) => wrap(o.size, `
            <path d="M4 14 L6 9 L18 9 L20 14 L20 17 L4 17 Z"/>
            <line x1="4" y1="14" x2="20" y2="14"/>
            <circle cx="8" cy="17" r="1.4"/>
            <circle cx="16" cy="17" r="1.4"/>`),
        permanent_station: (o = {}) => wrap(o.size, `
            <path d="M8 21 L12 4 L16 21"/>
            <line x1="6" y1="21" x2="18" y2="21"/>
            <line x1="9.5" y1="14" x2="14.5" y2="14"/>
            <circle cx="12" cy="4" r="1.2" fill="currentColor"/>`),
        uav: (o = {}) => wrap(o.size, `
            <rect x="10" y="10" width="4" height="4" rx="0.6"/>
            <line x1="10" y1="10" x2="6" y2="6"/>
            <line x1="14" y1="10" x2="18" y2="6"/>
            <line x1="10" y1="14" x2="6" y2="18"/>
            <line x1="14" y1="14" x2="18" y2="18"/>
            <ellipse cx="6" cy="6" rx="2.4" ry="1.2"/>
            <ellipse cx="18" cy="6" rx="2.4" ry="1.2"/>
            <ellipse cx="6" cy="18" rx="2.4" ry="1.2"/>
            <ellipse cx="18" cy="18" rx="2.4" ry="1.2"/>`),
        rod: (o = {}) => wrap(o.size, `
            <line x1="12" y1="3" x2="12" y2="21"/>
            <line x1="9" y1="6" x2="12" y2="6"/>
            <line x1="10" y1="10" x2="12" y2="10"/>
            <line x1="9" y1="14" x2="12" y2="14"/>
            <line x1="10" y1="18" x2="12" y2="18"/>`),
        man: (o = {}) => wrap(o.size, `
            <circle cx="12" cy="5" r="2.2"/>
            <path d="M12 7.5 L12 14"/>
            <path d="M12 14 L8 21"/>
            <path d="M12 14 L16 21"/>
            <path d="M8 10 L12 11.5 L16 10"/>`),

        // ── UI ACTIONS ──
        plus:    (o = {}) => wrap(o.size, `<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>`),
        edit:    (o = {}) => wrap(o.size, `<path d="M14 4 L20 10 L9 21 L3 21 L3 15 Z"/><line x1="14" y1="4" x2="20" y2="10"/>`),
        trash:   (o = {}) => wrap(o.size, `<path d="M4 7 L20 7"/><path d="M9 7 V4 H15 V7"/><path d="M6 7 L7 21 H17 L18 7"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/>`),
        check:   (o = {}) => wrap(o.size, `<polyline points="4 12 10 18 20 6"/>`),
        checkbox:(o = {}) => wrap(o.size, `<rect x="4" y="4" width="16" height="16" rx="2"/><polyline points="8 12 11 15 16 9"/>`),
        refresh: (o = {}) => wrap(o.size, `<path d="M3 12 A9 9 0 0 1 18 5.5 L21 8"/><polyline points="21 3 21 8 16 8"/><path d="M21 12 A9 9 0 0 1 6 18.5 L3 16"/><polyline points="3 21 3 16 8 16"/>`),
        moon:    (o = {}) => wrap(o.size, `<path d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z"/>`),
        sun:     (o = {}) => wrap(o.size, `<circle cx="12" cy="12" r="4"/><line x1="12" y1="2" x2="12" y2="5"/><line x1="12" y1="19" x2="12" y2="22"/><line x1="2" y1="12" x2="5" y2="12"/><line x1="19" y1="12" x2="22" y2="12"/><line x1="5" y1="5" x2="7" y2="7"/><line x1="17" y1="17" x2="19" y2="19"/><line x1="5" y1="19" x2="7" y2="17"/><line x1="17" y1="7" x2="19" y2="5"/>`),
        arrow_right: (o = {}) => wrap(o.size, `<line x1="5" y1="12" x2="19" y2="12"/><polyline points="13 5 20 12 13 19"/>`),
        arrow_left:  (o = {}) => wrap(o.size, `<line x1="19" y1="12" x2="5" y2="12"/><polyline points="11 5 4 12 11 19"/>`),
        swap_v:  (o = {}) => wrap(o.size, `<polyline points="7 4 7 20"/><polyline points="3 8 7 4 11 8"/><polyline points="17 4 17 20"/><polyline points="13 16 17 20 21 16"/>`),
        warn:    (o = {}) => wrap(o.size, `<path d="M12 4 L21 19 L3 19 Z"/><line x1="12" y1="10" x2="12" y2="14"/><circle cx="12" cy="16.5" r="0.9" fill="currentColor" stroke="none"/>`),
        ban:     (o = {}) => wrap(o.size, `<circle cx="12" cy="12" r="9"/><line x1="6" y1="6" x2="18" y2="18"/>`),
        user:    (o = {}) => wrap(o.size, `<circle cx="12" cy="8" r="3.5"/><path d="M5 21 C5 16 8 14 12 14 C16 14 19 16 19 21"/>`),
        close:   (o = {}) => wrap(o.size, `<line x1="6" y1="6" x2="18" y2="18"/><line x1="18" y1="6" x2="6" y2="18"/>`),
        bot:     (o = {}) => wrap(o.size, `<rect x="5" y="8" width="14" height="11" rx="2"/><circle cx="9.5" cy="13" r="0.9" fill="currentColor" stroke="none"/><circle cx="14.5" cy="13" r="0.9" fill="currentColor" stroke="none"/><line x1="12" y1="5" x2="12" y2="8"/><circle cx="12" cy="4" r="1" fill="currentColor" stroke="none"/>`),
        download:(o = {}) => wrap(o.size, `<line x1="12" y1="4" x2="12" y2="16"/><polyline points="6 11 12 17 18 11"/><line x1="4" y1="20" x2="20" y2="20"/>`),

        // ── DOMAIN-SPECIFIC ──
        target:  (o = {}) => wrap(o.size, `<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="5"/><circle cx="12" cy="12" r="1.5" fill="currentColor"/>`),
        clock:   (o = {}) => wrap(o.size, `<circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 16 14"/>`),
        pin:     (o = {}) => wrap(o.size, `<path d="M12 2 C8 2 5 5 5 9 C5 14 12 22 12 22 C12 22 19 14 19 9 C19 5 16 2 12 2 Z"/><circle cx="12" cy="9" r="2.5"/>`),
        sat:     (o = {}) => wrap(o.size, `<path d="M5 9 L7 5 L17 5 L19 9 L19 11 L5 11 Z"/><line x1="12" y1="11" x2="12" y2="20"/><line x1="9" y1="20" x2="15" y2="20"/><path d="M3 5 A4 4 0 0 1 7 1" opacity="0.6"/><path d="M2 9 A7 7 0 0 1 9 2" opacity="0.6"/>`),
        ruler:   (o = {}) => wrap(o.size, `<rect x="3" y="9" width="18" height="6" rx="1" transform="rotate(-30 12 12)"/>`),
        satellite:(o = {}) => wrap(o.size, `<rect x="9" y="9" width="6" height="6" rx="0.6" transform="rotate(45 12 12)"/><line x1="7" y1="7" x2="4" y2="4"/><line x1="17" y1="7" x2="20" y2="4"/><path d="M15 15 A5 5 0 0 1 9 21" opacity="0.7"/>`),
        calendar:(o = {}) => wrap(o.size, `<rect x="3" y="5" width="18" height="16" rx="2"/><line x1="3" y1="10" x2="21" y2="10"/><line x1="8" y1="3" x2="8" y2="7"/><line x1="16" y1="3" x2="16" y2="7"/>`),
        file:    (o = {}) => wrap(o.size, `<path d="M6 3 L14 3 L19 8 L19 21 L6 21 Z"/><polyline points="14 3 14 8 19 8"/>`),
    };

    window.ICONS = ICONS;
    /* Helper: replace text content of any element with [data-icon="name"] */
    window.applyIcons = function (root = document) {
        root.querySelectorAll('[data-icon]').forEach(el => {
            const name = el.dataset.icon;
            const size = el.dataset.iconSize ? Number(el.dataset.iconSize) : undefined;
            if (ICONS[name]) el.innerHTML = ICONS[name]({ size });
        });
    };
    document.addEventListener('DOMContentLoaded', () => window.applyIcons());
})();
