/* ── state ── */
let selectedDeviceType = null;
let selectedMountType  = null;

/* ── helpers ── */
function showAlert(msg) {
    const b = document.getElementById('alertBox');
    b.textContent = msg;
    b.className   = 'alert err';
    b.style.display = 'block';
}
function hideAlert() {
    const b = document.getElementById('alertBox');
    b.style.display = 'none';
}
function setErr(id, msg) {
    const el = document.getElementById('err-' + id);
    if (el) el.textContent = msg;
    const inp = document.getElementById(id);
    if (inp) inp.classList.toggle('err', !!msg);
}

function setProgress(step) {
    const pct = { 1: 33, 2: 66, 3: 100 };
    document.getElementById('progressFill').style.width = pct[step] + '%';

    for (let i = 1; i <= 3; i++) {
        const d = document.getElementById('dot' + i);
        d.classList.remove('active', 'done');
        if (i < step)      d.classList.add('done');
        else if (i === step) d.classList.add('active');
    }
    for (let i = 1; i <= 2; i++) {
        document.getElementById('sep' + i).classList.toggle('done', i < step);
    }

    document.querySelectorAll('.step').forEach(s => s.classList.remove('active'));
    const target = step === 4 ? 'stepOk' : 'step' + step;
    document.getElementById(target).classList.add('active');
}

/* ── avatar preview ── */
function previewAvatar(input) {
    const file = input.files[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = e => {
        const img = document.getElementById('avatarImg');
        img.src = e.target.result;
        img.style.display = 'block';
        document.querySelector('#avatarPreview .av-icon').style.display = 'none';
    };
    reader.readAsDataURL(file);
}

/* ── device type / mount ── */
function selectType(el) {
    document.querySelectorAll('.type-card').forEach(c => c.classList.remove('chosen'));
    el.classList.add('chosen');
    selectedDeviceType = el.dataset.type;
    document.getElementById('mountField').style.display = 'block';
    document.getElementById('descField').style.display = 'block';
}

function selectMount(el) {
    document.querySelectorAll('.mount-card').forEach(c => c.classList.remove('chosen'));
    el.classList.add('chosen');
    selectedMountType = el.dataset.mount;
}

/* ── navigation ── */
function goBack(step) {
    hideAlert();
    setProgress(step);
}

function goStep2() {
    hideAlert();
    const login    = document.getElementById('login').value.trim();
    const password = document.getElementById('password').value;
    const confirm  = document.getElementById('confirmPassword').value;

    let ok = true;
    setErr('login', ''); setErr('password', ''); setErr('confirm', '');

    if (login.length < 3) { setErr('login', 'Минимум 3 символа'); ok = false; }
    if (password.length < 6) { setErr('password', 'Минимум 6 символов'); ok = false; }
    if (password !== confirm) { setErr('confirm', 'Пароли не совпадают'); ok = false; }

    if (!ok) return;
    setProgress(2);
}

function goStep3() {
    hideAlert();
    setProgress(3);
}

/* ── submit ── */
async function submitRegistration(skipDevice = false) {
    hideAlert();

    const btn = document.getElementById('btnRegister');
    btn.disabled = true;
    btn.textContent = '⏳ Регистрация…';

    try {
        const formData = new FormData();
        formData.append('login',    document.getElementById('login').value.trim());
        formData.append('password', document.getElementById('password').value);

        const fullName = document.getElementById('fullName').value.trim();
        if (fullName) formData.append('fullName', fullName);

        const avatarFile = document.getElementById('avatarFile').files[0];
        if (avatarFile) formData.append('avatar', avatarFile);

        if (!skipDevice) {
            const deviceName = document.getElementById('deviceName').value.trim();
            if (deviceName && selectedDeviceType) {
                if (!selectedMountType) {
                    showAlert('Укажите, на чём установлено устройство, или пропустите этот шаг');
                    btn.disabled = false; btn.textContent = 'Зарегистрироваться';
                    return;
                }
                const device = {
                    name:        deviceName,
                    deviceType:  selectedDeviceType,
                    mountType:   selectedMountType,
                    description: document.getElementById('deviceDesc').value.trim(),
                };
                formData.append('device', JSON.stringify(device));
            }
        }

        const resp = await fetch('/api/register', { method: 'POST', body: formData });
        const data = await resp.json();

        if (!resp.ok) {
            showAlert(data.error || 'Ошибка регистрации');
            btn.disabled = false; btn.textContent = 'Зарегистрироваться';
            return;
        }

        /* success — auto login */
        const loginResp = await fetch('/api/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                login:    document.getElementById('login').value.trim(),
                password: document.getElementById('password').value,
            }),
        });
        if (loginResp.ok) {
            const loginData = await loginResp.json();
            localStorage.setItem('token',     loginData.token);
            localStorage.setItem('userLogin', loginData.login);
        }

        setProgress(4);
        setTimeout(() => { window.location.href = '/profile'; }, 1800);

    } catch (e) {
        showAlert('Ошибка соединения с сервером');
        btn.disabled = false; btn.textContent = 'Зарегистрироваться';
    }
}