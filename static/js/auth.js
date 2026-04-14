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
            setTimeout(() => { window.location.href = '/profile'; }, 1000);
        } else {
            errorDiv.textContent = data.error || 'Ошибка входа. Проверьте логин и пароль.';
            errorDiv.style.display = 'block';
        }
    } catch (error) {
        errorDiv.textContent = 'Ошибка соединения с сервером. Попробуйте позже.';
        errorDiv.style.display = 'block';
    }
});