/* ════════════════════════════════════════
   УТИЛИТЫ
════════════════════════════════════════ */
function showNotification(message, isError = false) {
    const n = document.getElementById('notification');
    n.textContent = message;
    n.style.background = isError ? '#fee2e2' : '#e6f7e6';
    n.style.color      = isError ? '#c53030' : '#2e7d32';
    n.style.display    = 'block';
    setTimeout(() => { n.style.display = 'none'; }, 3000);
}

function isAuthenticated() {
    return !!(localStorage.getItem('token') && localStorage.getItem('userLogin'));
}

function logout() {
    localStorage.removeItem('token');
    localStorage.removeItem('userLogin');
    // Сбрасываем кэш аватара
    localStorage.removeItem('_av_cache');
    localStorage.removeItem('_av_login');
    renderHeader(false);
    showNotification('Вы вышли из системы');
}

/* ════════════════════════════════════════
   АВАТАР — кэш в localStorage
════════════════════════════════════════ */
async function loadAvatar(login) {
    const cachedLogin  = localStorage.getItem('_av_login');
    const cachedAvatar = localStorage.getItem('_av_cache');

    // Возвращаем кэш если логин совпадает (даже если аватара нет — пустая строка)
    if (cachedLogin === login && cachedAvatar !== null) {
        return cachedAvatar;
    }

    try {
        const resp = await fetch('/api/profile/data', {
            headers: { 'Authorization': 'Bearer ' + localStorage.getItem('token') }
        });
        if (!resp.ok) throw new Error();
        const data   = await resp.json();
        const avatar = data.avatar || '';
        localStorage.setItem('_av_login', login);
        localStorage.setItem('_av_cache', avatar);
        return avatar;
    } catch {
        return '';
    }
}

/* ════════════════════════════════════════
   ШАПКА
════════════════════════════════════════ */
function makeAvatar(login, src) {
    const div = document.createElement('div');
    div.className = 'user-avatar' + (src ? '' : ' av-letter');
    div.title  = 'Личный кабинет';
    div.onclick = () => { window.location.href = '/profile'; };

    if (src) {
        const img = document.createElement('img');
        img.src = src; img.alt = login;
        div.appendChild(img);
    } else {
        div.textContent = login.charAt(0).toUpperCase();
    }
    return div;
}

function renderHeader(authenticated) {
    const wrap = document.getElementById('userInfo');
    wrap.innerHTML = '';

    if (!authenticated) {
        wrap.innerHTML = `
            <div class="auth-buttons">
                <a href="/login" class="login-link">Вход</a>
                <a href="/register" class="register-link">Регистрация</a>
            </div>`;
        return;
    }

    const login = localStorage.getItem('userLogin') || '';

    const nameEl = document.createElement('span');
    nameEl.className   = 'user-name';
    nameEl.textContent = login;

    // Сразу показываем инициал
    const avatarEl = makeAvatar(login, '');

    const logoutEl = document.createElement('button');
    logoutEl.className   = 'logout-btn';
    logoutEl.textContent = 'Выйти';
    logoutEl.onclick     = logout;

    wrap.appendChild(nameEl);
    wrap.appendChild(avatarEl);
    wrap.appendChild(logoutEl);

    // Асинхронно заменяем инициал на фото (если есть)
    loadAvatar(login).then(src => {
        if (src) avatarEl.replaceWith(makeAvatar(login, src));
    });
}

/* ════════════════════════════════════════
   НАВИГАЦИЯ
════════════════════════════════════════ */
document.querySelectorAll('.menu-item:not(.disabled)').forEach(item => {
    item.addEventListener('click', function (e) {
        e.preventDefault();
        const page        = this.dataset.page;
        const isProtected = this.dataset.protected === 'true';
        if (isProtected && !isAuthenticated()) {
            showNotification('Для доступа необходимо войти в систему', true);
            setTimeout(() => { window.location.href = '/login'; }, 1500);
            return;
        }
        window.location.href = page === 'measurements' ? '/measurements' : '/profile';
    });
});

/* ════════════════════════════════════════
   СТАТИСТИКА
════════════════════════════════════════ */
function animateValue(el, end, duration) {
    if (!el) return;
    let current = 0;
    const step  = end / (duration / 16);
    const timer = setInterval(() => {
        current += step;
        if (current >= end) { current = end; clearInterval(timer); }
        el.textContent = Math.round(current);
    }, 16);
}

async function loadStats() {
    try {
        const r = await fetch('/api/stats');
        if (r.ok) {
            const s = await r.json();
            animateValue(document.getElementById('activeUsers'),       s.activeUsers       || 0, 900);
            animateValue(document.getElementById('measurementsToday'), s.measurementsToday || 0, 900);
        }
    } catch { /* тихо */ }
}

/* ════════════════════════════════════════
   ИНИЦИАЛИЗАЦИЯ
════════════════════════════════════════ */
// Сбрасываем кэш аватара если логин сменился
(() => {
    const curLogin    = localStorage.getItem('userLogin');
    const cachedLogin = localStorage.getItem('_av_login');
    if (curLogin !== cachedLogin) {
        localStorage.removeItem('_av_cache');
        localStorage.removeItem('_av_login');
    }
})();

renderHeader(isAuthenticated());
loadStats();