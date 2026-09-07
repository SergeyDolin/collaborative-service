// Разрешаем только внутренние пути, чтобы ?back= не увёл на чужой домен.
function safeBackPath() {
    try {
        const q = new URLSearchParams(location.search);
        const back = q.get('back');
        if (!back) return '/profile';
        // Только относительные пути на своём origin.
        if (back.charAt(0) !== '/' || back.charAt(1) === '/') return '/profile';
        return back;
    } catch (_) { return '/profile'; }
}

// Показать плашку "сессия истекла" при переходе с ?expired=1
(function showExpiredHint() {
    const q = new URLSearchParams(location.search);
    if (q.get('expired') !== '1') return;
    const errorDiv = document.getElementById('errorMessage');
    if (!errorDiv) return;
    errorDiv.textContent = 'Сессия истекла. Пожалуйста, войдите снова.';
    errorDiv.style.display = 'block';
})();


document.getElementById('loginForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    const login = document.getElementById('login').value;
    const password = document.getElementById('password').value;
    const errorDiv = document.getElementById('errorMessage');
    const successDiv = document.getElementById('successMessage');
    errorDiv.style.display = 'none';
    successDiv.style.display = 'none';
    try {
        const response = await fetch('/api/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ login, password })
        });
        const data = await response.json();
        if (response.ok) {
            localStorage.setItem('token', data.token);
            localStorage.setItem('userLogin', data.login);
            successDiv.textContent = 'Вход выполнен успешно! Перенаправление...';
            successDiv.style.display = 'block';
            const dest = safeBackPath();
            setTimeout(() => { window.location.href = dest; }, 1000);
        } else {
            errorDiv.textContent = data.error || 'Ошибка входа. Проверьте логин и пароль.';
            errorDiv.style.display = 'block';
        }
    } catch (error) {
        errorDiv.textContent = 'Ошибка соединения с сервером. Попробуйте позже.';
        errorDiv.style.display = 'block';
    }
});