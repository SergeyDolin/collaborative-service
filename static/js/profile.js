/* ════════════════════════════════════════════
   STATE
════════════════════════════════════════════ */
const STATIC_CRS = new Set(['ГСК-2011','СК-42','СК-95','ПЗ-90','ПЗ-90.02','ПЗ-90.11','WGS84(G1150)']);
let profileData = null;
let currentTransformCoords = null;
let pendingConfirmFn = null;
let selectMode = false;
let selectedIds = new Set();
let newDevType   = null;
let newMountType = null;
let newPcMethod  = null;
let isHistLoading = false;

/* ════════════════════════════════════════════
   COORDINATE HELPERS
════════════════════════════════════════════ */

// BLH → ECEF (WGS-84)
function blhToECEF(lat, lon, h) {
    const a  = 6378137.0, f = 1 / 298.257223563;
    const e2 = 2 * f - f * f;
    const latR = lat * Math.PI / 180;
    const lonR = lon * Math.PI / 180;
    const N = a / Math.sqrt(1 - e2 * Math.sin(latR) ** 2);
    return {
        x: (N + h) * Math.cos(latR) * Math.cos(lonR),
        y: (N + h) * Math.cos(latR) * Math.sin(lonR),
        z: (N * (1 - e2) + h) * Math.sin(latR)
    };
}

// σENU → σXYZ (диагональные элементы ковариационной матрицы)
function enuSigmaToECEF(lat, lon, sE, sN, sU) {
    const latR = lat * Math.PI / 180, lonR = lon * Math.PI / 180;
    const sl = Math.sin(latR), cl = Math.cos(latR);
    const sp = Math.sin(lonR), cp = Math.cos(lonR);
    const vE = sE*sE, vN = sN*sN, vU = sU*sU;
    return {
        x: Math.sqrt(sp*sp*vE + sl*sl*cp*cp*vN + cl*cl*cp*cp*vU),
        y: Math.sqrt(cp*cp*vE + sl*sl*sp*sp*vN + cl*cl*sp*sp*vU),
        z: Math.sqrt(cl*cl*vN + sl*sl*vU)
    };
}

// σN(м) → σB(угл.сек),  σE(м) → σL(угл.сек)  на эллипсоиде WGS-84
function metersToDeg(lat, sN, sE) {
    const a  = 6378137.0, f = 1 / 298.257223563;
    const e2 = 2 * f - f * f;
    const latR = lat * Math.PI / 180;
    const sin2 = Math.sin(latR) ** 2;
    const M = a * (1 - e2) / Math.pow(1 - e2 * sin2, 1.5);          // радиус кривизны в меридиане
    const N = a / Math.sqrt(1 - e2 * sin2);                          // радиус кривизны в первом вертикале
    const rad2sec = 180 / Math.PI * 3600;
    return {
        sB: sN / M * rad2sec,                                         // угл.сек
        sL: sE / (N * Math.cos(latR)) * rad2sec                      // угл.сек
    };
}

// Построить HTML-блок координат: BLH и XYZ сразу, СКП из sdn/sde/sdu (RTKLIB).
// isKinOrAbs=true → показывать среднее СКП одной строкой (кинематика / абсолютный метод).
// isKinOrAbs=false → показывать СКП отдельно по каждой оси XYZ (статика PPP).
function buildCoordsHtml(lat, lon, h, sN, sE, sU, isKinOrAbs = false) {
    const fmtDeg = v => v.toFixed(8) + '°';
    const fmtM   = v => v.toFixed(4) + ' м';
    const skpM   = v => v > 0 ? `<span class="coords-skp"> σ ${v.toFixed(4)} м</span>` : '';

    const hasSKP = sN > 0 && sE > 0 && sU > 0;
    const xyz    = blhToECEF(lat, lon, h);
    const sXYZ   = hasSKP ? enuSigmaToECEF(lat, lon, sE, sN, sU) : null;

    const xyzBlock = `
        <div class="coords-row">
            <span class="coords-label">X</span>
            <span class="coords-val">${fmtM(xyz.x)}${sXYZ ? skpM(sXYZ.x) : ''}</span>
        </div>
        <div class="coords-row">
            <span class="coords-label">Y</span>
            <span class="coords-val">${fmtM(xyz.y)}${sXYZ ? skpM(sXYZ.y) : ''}</span>
        </div>
        <div class="coords-row">
            <span class="coords-label">Z</span>
            <span class="coords-val">${fmtM(xyz.z)}${sXYZ ? skpM(sXYZ.z) : ''}</span>
        </div>`;

    const note = hasSKP
        ? `<div class="coords-note">σ — стандартное отклонение по внутренней сходимости${isKinOrAbs ? ', среднее по эпохам' : ''}</div>`
        : '';

    return `<div class="coords-both">
        <div class="coords-section">
            <div class="coords-row">
                <span class="coords-label">B</span>
                <span class="coords-val">${fmtDeg(lat)}</span>
            </div>
            <div class="coords-row">
                <span class="coords-label">L</span>
                <span class="coords-val">${fmtDeg(lon)}</span>
            </div>
            <div class="coords-row">
                <span class="coords-label">H</span>
                <span class="coords-val">${fmtM(h)}</span>
            </div>
        </div>
        <div class="coords-section">
            ${xyzBlock}
        </div>
    </div>${note}`;
}

/* ════════════════════════════════════════════
   TASK POLLING
════════════════════════════════════════════ */
const _pollingTimers = {};   // taskId → intervalId

function stopPolling(taskId) {
    if (_pollingTimers[taskId]) {
        clearInterval(_pollingTimers[taskId]);
        delete _pollingTimers[taskId];
    }
}

function stopAllPolling() {
    Object.keys(_pollingTimers).forEach(stopPolling);
}

// Запустить опрос статуса задачи каждые 3 сек
function startPolling(taskId) {
    if (_pollingTimers[taskId]) return;
    _pollingTimers[taskId] = setInterval(() => pollTaskStatus(taskId), 3000);
}

async function pollTaskStatus(taskId) {
    try {
        const r = await fetch(`/api/measurements/status?id=${taskId}`, {
            headers: { 'Authorization': `Bearer ${getToken()}` }
        });
        if (!r.ok) { stopPolling(taskId); return; }
        const data = await r.json();
        const status = data.status;

        const item = document.querySelector(`.history-item[data-task-id="${taskId}"]`);
        if (!item) { stopPolling(taskId); return; }

        // Обновляем бейдж статуса
        const badge = item.querySelector('.status-badge');
        if (badge) {
            badge.className = `status-badge status-${status}`;
            badge.textContent = getStatusText(status);
        }

        if (status === 'completed' || status === 'failed') {
            stopPolling(taskId);
            // Полностью перерисовываем историю чтобы показать результат
            loadHistory();
        } else {
            // Обновляем label прогресс-бара
            const label = item.querySelector('.task-progress-label');
            if (label) label.textContent = getProgressLabel(status, data.processingSec);
        }
    } catch { /* игнорируем сетевые ошибки */ }
}

function getProgressLabel(status, sec) {
    if (status === 'pending')    return 'В очереди…';
    if (status === 'processing') return sec ? `Обработка ${sec.toFixed(0)} с…` : 'Обработка…';
    return '';
}

// Анимация: спутник болтается над приёмником
function buildProgressHtml(status) {
    const label = getProgressLabel(status, null);
    return `<div class="task-progress">
        <div class="task-progress-anim">
            <svg class="sat-scene" viewBox="0 0 90 54" fill="none" xmlns="http://www.w3.org/2000/svg">
                <!-- Приёмник -->
                <!-- Основание -->
                <rect x="14" y="43" width="20" height="3" rx="1.5" fill="currentColor" opacity="0.6"/>
                <!-- Стойка -->
                <rect x="22" y="36" width="4" height="8" rx="1" fill="currentColor" opacity="0.55"/>
                <!-- Чаша антенны (дуга) -->
                <path d="M10 36 Q24 26 38 36" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" fill="none" opacity="0.65"/>
                <!-- Фид (центр чаши) -->
                <circle cx="24" cy="32" r="2.2" fill="currentColor" opacity="0.8"/>
                <!-- Луч сигнала + спутник — вращаются вместе от центра чаши -->
                <g class="sat-signal">
                    <!-- луч пунктиром от фида вверх -->
                    <line x1="24" y1="30" x2="24" y2="10" stroke="currentColor" stroke-width="1" stroke-dasharray="2.5 2.5" opacity="0.7"/>
                </g>
                <g class="sat-swing">
                    <!-- спутник на конце "нити" длиной ~36px от точки вращения (24,46) -->
                    <g transform="translate(24,10)">
                        <!-- корпус -->
                        <rect x="-5" y="-4" width="10" height="8" rx="1.5" fill="currentColor" opacity="0.85"/>
                        <!-- левая панель -->
                        <rect x="-14" y="-2" width="8" height="4" rx="1" fill="currentColor" opacity="0.5"/>
                        <line x1="-6" y1="0" x2="-14" y2="0" stroke="currentColor" stroke-width="0.8" opacity="0.4"/>
                        <!-- правая панель -->
                        <rect x="6"  y="-2" width="8" height="4" rx="1" fill="currentColor" opacity="0.5"/>
                        <line x1="6"  y1="0" x2="14" y2="0" stroke="currentColor" stroke-width="0.8" opacity="0.4"/>
                        <!-- антенна вниз (к приёмнику) -->
                        <line x1="0" y1="4" x2="0" y2="8" stroke="currentColor" stroke-width="1" opacity="0.6"/>
                        <circle cx="0" cy="9" r="1.2" fill="currentColor" opacity="0.7"/>
                    </g>
                </g>
            </svg>
        </div>
        <div class="task-progress-label">${label}</div>
    </div>`;
}

/* ════════════════════════════════════════════
   AUTH
════════════════════════════════════════════ */
function getToken()  { return localStorage.getItem('token'); }
function getLogin()  { return localStorage.getItem('userLogin') || ''; }
function logout()    { localStorage.removeItem('token'); localStorage.removeItem('userLogin'); window.location.href = '/'; }

function checkAuth() {
    if (!getToken() || !getLogin()) { window.location.href = '/login'; return false; }
    document.getElementById('headerLogin').textContent = getLogin();
    return true;
}

/* ════════════════════════════════════════════
   TOAST
════════════════════════════════════════════ */
let _toastTimer;
function showToast(msg, type = 'ok') {
    const t = document.getElementById('toast');
    t.textContent = msg; t.className = `toast ${type} show`;
    clearTimeout(_toastTimer);
    _toastTimer = setTimeout(() => { t.className = 'toast'; }, 3000);
}

/* ════════════════════════════════════════════
   MODALS
════════════════════════════════════════════ */
function openModal(id)  { document.getElementById(id).classList.add('visible'); }
function closeModal(id) { document.getElementById(id).classList.remove('visible'); }
document.querySelectorAll('.modal-overlay').forEach(o => {
    o.addEventListener('click', e => { if (e.target === o) o.classList.remove('visible'); });
});

/* ════════════════════════════════════════════
   PROFILE LOAD
════════════════════════════════════════════ */
async function loadProfile() {
    try {
        const r = await fetch('/api/profile/data', { headers: { 'Authorization': `Bearer ${getToken()}` } });
        if (!r.ok) return;
        profileData = await r.json();
        renderProfile(profileData);
        renderDevices(profileData.devices || []);
    } catch(e) { console.error(e); }
}

function renderProfile(p) {
    const login = p.login || getLogin();
    document.getElementById('heroLogin').textContent = '@' + login;
    document.getElementById('heroName').textContent  = login;
    document.getElementById('headerLogin').textContent = login;

    if (p.createdAt) {
        const d = new Date(p.createdAt).toLocaleDateString('ru', { year:'numeric', month:'long' });
        document.getElementById('heroMeta').textContent = 'Участник с ' + d;
    }

    // Аватар генерируется сервером из логина — просто подставляем URL
    const avatarUrl = `/api/avatar?login=${encodeURIComponent(login)}`;
    const heroImg = document.getElementById('heroAvatarImg');
    if (heroImg) {
        heroImg.src = avatarUrl;
        heroImg.style.display = 'block';
        const fb = document.getElementById('heroAvatarFallback');
        if (fb) fb.style.display = 'none';
    }
}

/* ════════════════════════════════════════════
   DEVICES
════════════════════════════════════════════ */
const DEVICE_ICONS  = { gnss_receiver:'gnss_receiver', smartphone:'smartphone', tablet:'tablet', other:'other' };
const DEVICE_LABELS = { gnss_receiver:'ГНСС-приёмник', smartphone:'Смартфон', tablet:'Планшет', other:'Иное' };
const MOUNT_LABELS  = { car:'Автомобиль', permanent_station:'Пост. станция', uav:'БПЛА', rod:'Веха',  man: "Человек" };
function icon(name, size = 18) { return (window.ICONS && window.ICONS[name]) ? window.ICONS[name]({ size }) : ''; }

function renderDeviceExtra(d) {
    if (d.deviceType === 'gnss_receiver') {
        if (!d.antennaName) return '';
        const enu = (d.antennaE || d.antennaN || d.antennaU)
            ? `<div class="dev-antenna-enu">ENU: ${(+d.antennaE).toFixed(3)} / ${(+d.antennaN).toFixed(3)} / ${(+d.antennaU).toFixed(3)} м</div>`
            : '';
        let rcvHtml = '';
        if (d.receiverHost) {
            const rcvType = d.receiverType || 'tcp';
            const rcvAddr = rcvType === 'ntrip'
                ? `NTRIP ${escHtml(d.receiverHost)}:${d.receiverPort}/${escHtml(d.receiverMount || '')}`
                : rcvType === 'serial'
                ? `Serial ${escHtml(d.receiverHost)}`
                : `TCP ${escHtml(d.receiverHost)}:${d.receiverPort}`;
            rcvHtml = `<div class="dev-antenna-enu">${rcvAddr}</div>`;
        }
        return `<div class="dev-antenna">${escHtml(d.antennaName)}</div>${enu}${rcvHtml}`;
    }
    if (d.phaseCenterMethod === 'auto' && d.phaseCenterValidUntil) {
        const until = new Date(d.phaseCenterValidUntil);
        const now   = new Date();
        const expired = until < now;
        const fmt = until.toLocaleString('ru', { day:'2-digit', month:'2-digit', hour:'2-digit', minute:'2-digit' });
        return `<div class="dev-pc ${expired ? 'dev-pc-expired' : 'dev-pc-ok'}">
            <span class="icon">${expired ? icon('warn',14) : icon('bot',14)}</span>${expired ? 'Калибровка истекла' : 'Авто до ' + fmt}
        </div>`;
    }
    if (d.phaseCenterMethod === 'manual' && (d.antennaE || d.antennaN || d.antennaU)) {
        return `<div class="dev-antenna-enu">ENU: ${(+d.antennaE).toFixed(3)} / ${(+d.antennaN).toFixed(3)} / ${(+d.antennaU).toFixed(3)} м</div>`;
    }
    return '';
}

function renderDevices(devices) {
    window._profileDevices = devices || [];
    const el = document.getElementById('devicesContent');
    if (!devices || devices.length === 0) {
        el.innerHTML = `<div class="no-devices"><span class="nd-icon">${icon('gnss_receiver', 36)}</span>Нет зарегистрированных устройств.<br>Добавьте устройство для участия в коллаборативном позиционировании.</div>`;
        return;
    }
    el.innerHTML = `<div class="devices-grid">${devices.map(d => `
        <div class="device-card">
            <button class="dev-edit"   onclick="openEditDevice(${d.id})" title="Редактировать">${icon('edit',14)}</button>
            <button class="dev-delete" onclick="deleteDevice(${d.id})"   title="Удалить">${icon('close',14)}</button>
            <span class="dev-icon">${icon(DEVICE_ICONS[d.deviceType] || 'other', 22)}</span>
            <div class="dev-name">${escHtml(d.name)}</div>
            <div class="dev-badges">
                <span class="badge badge-type">${DEVICE_LABELS[d.deviceType] || d.deviceType}</span>
                <span class="badge badge-mount">${MOUNT_LABELS[d.mountType] || d.mountType}</span>
            </div>
            ${renderDeviceExtra(d)}
            ${d.description ? `<div class="dev-desc">${escHtml(d.description)}</div>` : ''}
        </div>`).join('')}
        <button class="add-device-card" onclick="openAddDevice()">
            <span class="add-icon">${icon('plus',22)}</span><span>Добавить устройство</span>
        </button>
    </div>`;
}

let editingDeviceId = null;

function resetDeviceModal() {
    newDevType = null; newMountType = null; newPcMethod = null;
    editingDeviceId = null;
    document.querySelectorAll('.tc').forEach(c => c.classList.remove('chosen'));
    document.querySelectorAll('.mc').forEach(c => c.classList.remove('chosen'));
    document.querySelectorAll('.pc-card').forEach(c => c.classList.remove('chosen'));
    document.getElementById('newDevName').value     = '';
    document.getElementById('newDevDesc').value     = '';
    document.getElementById('devAntennaName').value = '';
    document.getElementById('devAntennaE').value    = '0';
    document.getElementById('devAntennaN').value    = '0';
    document.getElementById('devAntennaU').value    = '0';
    document.getElementById('devPcE').value = '0';
    document.getElementById('devPcN').value = '0';
    document.getElementById('devPcU').value = '0';
    document.getElementById('devAntennaField').style.display       = 'none';
    document.getElementById('devAntennaOffsetField').style.display = 'none';
    document.getElementById('devPhaseCenterField').style.display   = 'none';
    document.getElementById('devAutoWarning').style.display        = 'none';
    document.getElementById('devManualOffsets').style.display      = 'none';
    document.getElementById('gnssReceiverSection').style.display   = 'none';
    document.getElementById('devReceiverType').value  = 'tcp';
    document.getElementById('devReceiverHost').value  = '';
    document.getElementById('devReceiverPort').value  = '';
    document.getElementById('ntripFields').style.display = 'none';
    document.getElementById('devReceiverMount').value = '';
    document.getElementById('devReceiverUser').value  = '';
    document.getElementById('devReceiverPass').value  = '';
    document.getElementById('addDeviceModalTitle').textContent = 'Добавить устройство';
}

function openAddDevice() {
    resetDeviceModal();
    openModal('addDeviceModal');
}

function openEditDevice(id) {
    const d = (window._profileDevices || []).find(x => x.id === id);
    if (!d) return;
    resetDeviceModal();
    editingDeviceId = id;
    document.getElementById('addDeviceModalTitle').textContent = 'Редактировать устройство';
    document.getElementById('newDevName').value = d.name || '';
    document.getElementById('newDevDesc').value = d.description || '';

    // Тип устройства
    const tcEl = document.querySelector(`.tc[data-type="${d.deviceType}"]`);
    if (tcEl) { tcEl.classList.add('chosen'); newDevType = d.deviceType; }

    // Тип установки
    const mcEl = document.querySelector(`.mc[data-mount="${d.mountType}"]`);
    if (mcEl) { mcEl.classList.add('chosen'); newMountType = d.mountType; }

    if (d.deviceType === 'gnss_receiver') {
        document.getElementById('devAntennaField').style.display       = 'block';
        document.getElementById('devAntennaOffsetField').style.display = 'block';
        document.getElementById('gnssReceiverSection').style.display   = 'block';
        document.getElementById('devAntennaName').value = d.antennaName || '';
        document.getElementById('devAntennaE').value    = d.antennaE ?? 0;
        document.getElementById('devAntennaN').value    = d.antennaN ?? 0;
        document.getElementById('devAntennaU').value    = d.antennaU ?? 0;
        document.getElementById('devReceiverType').value = d.receiverType || 'tcp';
        document.getElementById('devReceiverHost').value = d.receiverHost || '';
        document.getElementById('devReceiverPort').value = d.receiverPort || '';
        document.getElementById('devReceiverMount').value = d.receiverMount || '';
        document.getElementById('devReceiverUser').value  = d.receiverUser  || '';
        document.getElementById('devReceiverPass').value  = d.receiverPass  || '';
        toggleReceiverFields();
    } else {
        document.getElementById('devPhaseCenterField').style.display = 'block';
        const pcEl = document.querySelector(`.pc-card[data-method="${d.phaseCenterMethod}"]`);
        if (pcEl) { pcEl.classList.add('chosen'); newPcMethod = d.phaseCenterMethod; }
        if (d.phaseCenterMethod === 'manual') {
            document.getElementById('devManualOffsets').style.display = 'block';
            document.getElementById('devPcE').value = d.antennaE ?? 0;
            document.getElementById('devPcN').value = d.antennaN ?? 0;
            document.getElementById('devPcU').value = d.antennaU ?? 0;
        }
    }
    openModal('addDeviceModal');
}
function pickType(el) {
    document.querySelectorAll('.tc').forEach(c => c.classList.remove('chosen'));
    el.classList.add('chosen');
    newDevType = el.dataset.type;

    const isGNSS = newDevType === 'gnss_receiver';
    document.getElementById('devAntennaField').style.display       = isGNSS ? 'block' : 'none';
    document.getElementById('devAntennaOffsetField').style.display = isGNSS ? 'block' : 'none';
    document.getElementById('devPhaseCenterField').style.display   = isGNSS ? 'none'  : 'block';
    document.getElementById('gnssReceiverSection').style.display   = isGNSS ? 'block' : 'none';

    newPcMethod = null;
    document.querySelectorAll('.pc-card').forEach(c => c.classList.remove('chosen'));
    document.getElementById('devAutoWarning').style.display   = 'none';
    document.getElementById('devManualOffsets').style.display = 'none';
}
function toggleReceiverFields() {
    const type = document.getElementById('devReceiverType').value;
    const isNtrip  = type === 'ntrip';
    const isSerial = type === 'serial';
    document.getElementById('ntripFields').style.display = isNtrip ? 'block' : 'none';
    const label = document.getElementById('devHostLabel');
    if (isSerial) {
        label.textContent = 'Порт устройства / Скорость (бод)';
        document.getElementById('devReceiverHost').placeholder = '/dev/ttyUSB0';
        document.getElementById('devReceiverPort').placeholder = '115200';
    } else if (isNtrip) {
        label.textContent = 'Хост NTRIP / Порт';
        document.getElementById('devReceiverHost').placeholder = 'caster.example.com';
        document.getElementById('devReceiverPort').placeholder = '2101';
    } else {
        label.textContent = 'Хост / Порт устройства';
        document.getElementById('devReceiverHost').placeholder = '192.168.1.100';
        document.getElementById('devReceiverPort').placeholder = '9001';
    }
}
function pickMount(el) {
    document.querySelectorAll('.mc').forEach(c => c.classList.remove('chosen'));
    el.classList.add('chosen'); newMountType = el.dataset.mount;
}
function pickPcMethod(el) {
    document.querySelectorAll('.pc-card').forEach(c => c.classList.remove('chosen'));
    el.classList.add('chosen');
    newPcMethod = el.dataset.method;
    document.getElementById('devAutoWarning').style.display   = newPcMethod === 'none'   ? 'block' : 'none';
    document.getElementById('devManualOffsets').style.display = newPcMethod === 'manual' ? 'block' : 'none';
}
async function saveDevice() {
    const name = document.getElementById('newDevName').value.trim();
    if (!name)        { showToast('Введите название устройства', 'err'); return; }
    if (!newDevType)  { showToast('Выберите тип устройства', 'err'); return; }
    if (!newMountType){ showToast('Выберите тип установки', 'err'); return; }

    const payload = {
        name,
        deviceType:  newDevType,
        mountType:   newMountType,
        description: document.getElementById('newDevDesc').value.trim(),
    };

    if (newDevType === 'gnss_receiver') {
        const antennaName = document.getElementById('devAntennaName').value.trim();
        if (!antennaName) { showToast('Введите название антенны в формате RINEX', 'err'); return; }
        payload.antennaName = antennaName;
        payload.antennaE = parseFloat(document.getElementById('devAntennaE').value) || 0;
        payload.antennaN = parseFloat(document.getElementById('devAntennaN').value) || 0;
        payload.antennaU = parseFloat(document.getElementById('devAntennaU').value) || 0;
        payload.receiverType = document.getElementById('devReceiverType').value || 'tcp';
        payload.receiverHost = document.getElementById('devReceiverHost').value.trim();
        payload.receiverPort = parseInt(document.getElementById('devReceiverPort').value) || 0;
        if (payload.receiverType === 'ntrip') {
            payload.receiverMount = document.getElementById('devReceiverMount').value.trim();
            payload.receiverUser  = document.getElementById('devReceiverUser').value.trim();
            payload.receiverPass  = document.getElementById('devReceiverPass').value;
        }
    } else {
        if (!newPcMethod) { showToast('Укажите метод определения фазового центра', 'err'); return; }
        payload.phaseCenterMethod = newPcMethod;
        if (newPcMethod === 'manual') {
            const e = parseFloat(document.getElementById('devPcE').value);
            const n = parseFloat(document.getElementById('devPcN').value);
            const u = parseFloat(document.getElementById('devPcU').value);
            if (!e && !n && !u) { showToast('Введите хотя бы одно ненулевое смещение ENU', 'err'); return; }
            payload.antennaE = e || 0;
            payload.antennaN = n || 0;
            payload.antennaU = u || 0;
        }
    }

    try {
        const isEdit = !!editingDeviceId;
        const url    = isEdit ? `/api/devices?id=${editingDeviceId}` : '/api/devices';
        const r = await fetch(url, {
            method: isEdit ? 'PUT' : 'POST',
            headers: { 'Authorization': `Bearer ${getToken()}`, 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        if (!r.ok) {
            const err = await r.json().catch(() => ({}));
            showToast(err.error || (isEdit ? 'Ошибка сохранения' : 'Ошибка добавления'), 'err');
            return;
        }
        closeModal('addDeviceModal');
        showToast(isEdit ? 'Устройство сохранено' : 'Устройство добавлено');
        loadProfile();
    } catch { showToast('Ошибка добавления', 'err'); }
}
function deleteDevice(id) {
    openConfirm('Удалить устройство?', 'Устройство будет удалено безвозвратно.', async () => {
        try {
            await fetch(`/api/devices?id=${id}`, { method: 'DELETE', headers: { 'Authorization': `Bearer ${getToken()}` } });
            showToast('Устройство удалено');
            loadProfile();
        } catch { showToast('Ошибка', 'err'); }
    });
}

/* ════════════════════════════════════════════
   EDIT PROFILE  (только смена пароля)
════════════════════════════════════════════ */
function openEditProfile() { openModal('editProfileModal'); }

/* ════════════════════════════════════════════
   CONFIRM MODAL
════════════════════════════════════════════ */
function openConfirm(title, text, fn) {
    pendingConfirmFn = fn;
    document.getElementById('confirmTitle').textContent = title;
    document.getElementById('confirmText').textContent  = text;
    openModal('confirmModal');
}
document.getElementById('confirmBtn').addEventListener('click', () => {
    if (!pendingConfirmFn) return;
    const fn = pendingConfirmFn; closeModal('confirmModal'); fn();
});

/* ════════════════════════════════════════════
   HISTORY
════════════════════════════════════════════ */
function escHtml(s) {
    return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function formatCoord(v) { const n = Number(v); return isNaN(n) ? '—' : n.toFixed(8); }
function formatH(v)     { const n = Number(v); return isNaN(n) ? '—' : n.toFixed(3); }
function getMethodName(m) {
    return { absolute:'SPP', single:'SPP', relative:'Относительный', ppp:'PPP-AR' }[m?.toLowerCase()] || m || '—';
}
function getStatusText(s) {
    return { pending:'⏳ В очереди', processing:'🔄 Обработка…', completed:'✅ Завершено', failed:'❌ Ошибка' }[s] || s;
}
function getSolutionStatus(q) {
    if (q===1) return '<span class="fix-badge">FIX</span>';
    if (q===6) return '<span class="float-badge">FLOAT</span>';
    if (q===0) return '<span style="color:var(--err)">NO SOLUTION</span>';
    if (q)     return `<span>Q=${q}</span>`;
    return '';
}

function updateStats(tasks) {
    const done = tasks.filter(t => t.status === 'completed' && t.result);
    const fixSum = done.reduce((a, t) => a + (t.result.fixRate || 0), 0);
    const avg = done.length ? (fixSum / done.length).toFixed(1) : '0.0';
    document.getElementById('statCompleted').textContent = done.length;
    document.getElementById('statFix').textContent = avg + '%';
    document.getElementById('statFixBar').style.width = avg + '%';
    document.getElementById('statsGrid').style.display = done.length ? 'grid' : 'none';
}

async function loadHistory() {
    if (isHistLoading) return; isHistLoading = true;
    const el = document.getElementById('historyList');
    el.innerHTML = '<div class="loading-hist">⏳ Загрузка…</div>';
    try {
        const r = await fetch('/api/measurements/history?limit=50&offset=0', {
            headers: { 'Authorization': `Bearer ${getToken()}` }
        });
        if (!r.ok) {
            el.innerHTML = r.status === 401 ? '<div class="empty-history">🔒 Сессия истекла</div>' : '<div class="empty-history">❌ Ошибка загрузки</div>';
            return;
        }
        let tasks = await r.json();
        if (!tasks || tasks.length === 0) {
            el.innerHTML = '<div class="empty-history">📭 История обработок пуста</div>';
            document.getElementById('btnDeleteAll').style.display = 'none';
            document.getElementById('statsGrid').style.display = 'none';
            return;
        }
        document.getElementById('btnDeleteAll').style.display = selectMode ? 'none' : 'block';

        // Задачи, упавшие из-за неопубликованных Ultra-Rapid продуктов,
        // не показываем в истории — вместо этого рендерим баннер сверху
        // и удаляем их из БД (это не пользовательская ошибка, а «данные
        // за эту дату ещё не вышли»).
        const unavailableMark = 'PRODUCTS_UNAVAILABLE:';
        const unavailable = tasks.filter(t => t.status === 'failed'
            && typeof t.errorMessage === 'string'
            && t.errorMessage.startsWith(unavailableMark));
        tasks = tasks.filter(t => !unavailable.includes(t));
        renderUnavailableBanners(unavailable, unavailableMark);

        if (tasks.length === 0) {
            el.innerHTML = '<div class="empty-history">📭 История обработок пуста</div>';
            document.getElementById('btnDeleteAll').style.display = 'none';
            document.getElementById('statsGrid').style.display = 'none';
            return;
        }

        updateStats(tasks);
        // Сохраняем задачи для доступа из generateReport
        window._histTasks = {};
        tasks.forEach(t => { window._histTasks[t.id] = t; });
        el.innerHTML = tasks.map(task => {
            const date   = new Date(task.createdAt).toLocaleString('ru');
            const method = getMethodName(task.config?.method);
            const mode   = (task.fileType==='static'||task.config?.mode==='static') ? 'Статика' : 'Кинематика';
            const status = task.status || 'pending';
            let resultHtml = '';
            if (status==='completed' && task.result) {
                const r = task.result;
                const hasCoords = r.latitude || r.longitude;
                const fixRate = r.fixRate ? r.fixRate.toFixed(1) : null;
                let coordsHtml = '';
                // Извлекаем координаты и СКП из lastSolutionLine (формат RTKLIB .pos)
                // Колонки: date time lat lon h Q ns sdn sde sdu ...
                let lat = r.latitude, lon = r.longitude, h = r.height || 0;
                let sN = 0, sE = 0, sU = 0;
                const isKinOrAbs = task.config?.mode === 'kinematic' || task.config?.method === 'single';
                if (r.lastSolutionLine) {
                    const f = r.lastSolutionLine.trim().split(/\s+/);
                    if (f[2]) lat = parseFloat(f[2]);
                    if (f[3]) lon = parseFloat(f[3]);
                    if (f[4]) h   = parseFloat(f[4]);
                    // Берём σ из последней строки как базовые значения
                    if (f[7]) sN = parseFloat(f[7]);
                    if (f[8]) sE = parseFloat(f[8]);
                    if (f[9]) sU = parseFloat(f[9]);
                }
                // Кинематика/абсолютный: предпочитаем среднее σ из БД (если есть)
                if (isKinOrAbs && r.sdx > 0 && r.sdy > 0 && r.sdz > 0) {
                    sN = r.sdx;
                    sE = r.sdy;
                    sU = r.sdz;
                }
                if (lat && lon) {
                    coordsHtml = buildCoordsHtml(lat, lon, h, sN, sE, sU, isKinOrAbs);
                }
                const dlBtn = task.fileType !== 'static'
                    ? `<button class="download-btn" onclick="downloadResult('${task.id}',event)"><span data-icon="download" data-icon-size="14"></span> Скачать .pos</button>` : '';
                const trBtn = hasCoords
                    ? `<button class="btn-transform" onclick="openTransform(${r.latitude},${r.longitude},${r.height||0},'${task.id}')"><span data-icon="refresh" data-icon-size="14"></span> Пересчёт</button>` : '';
                const repBtn = `<button class="btn-report" onclick="generateReport('${task.id}', _histTasks['${task.id}'])"><span data-icon="file" data-icon-size="14"></span> Отчёт</button>`;
                resultHtml = `<div class="result-block">
                    <div class="stats-info">${getSolutionStatus(r.q)}${fixRate?` <span>(${fixRate}%)</span>`:''} ${r.nSat?`<span><span data-icon="satellite" data-icon-size="12"></span> ${r.nSat}</span>`:''}</div>
                    ${coordsHtml}
                    <div class="action-buttons">${dlBtn}${trBtn}${repBtn}</div>
                </div>`;
            }
            const inProgress = status === 'pending' || status === 'processing';
            const progressHtml = inProgress ? buildProgressHtml(status) : '';
            const errHtml = (status==='failed'&&task.errorMessage) ? `<div class="error-msg">❌ ${escHtml(task.errorMessage)}</div>` : '';
            return `<div class="history-item" data-task-id="${task.id}">
                <input type="checkbox" class="task-checkbox" data-id="${task.id}" onchange="onCbChange(this)">
                <div class="item-body">
                    <div class="history-header-row">
                        <div class="history-info">
                            <div class="history-date"><span data-icon="calendar" data-icon-size="12"></span> ${date}</div>
                            <div class="history-method">${method} · ${mode}</div>
                            <div class="history-file"><span data-icon="file" data-icon-size="12"></span> ${escHtml(task.filename||'—')}</div>
                        </div>
                        <div class="history-status">
                            <span class="status-badge status-${status}">${getStatusText(status)}</span>
                            <button class="btn-delete-single" onclick="confirmDeleteOne('${task.id}')"><span data-icon="trash" data-icon-size="12"></span> Удалить</button>
                        </div>
                    </div>
                    ${progressHtml}${resultHtml}${errHtml}
                </div>
            </div>`;
        }).join('');
        if (window.applyIcons) window.applyIcons(el);
        if (selectMode) el.classList.add('select-mode');

        // Запускаем polling для всех незавершённых задач
        stopAllPolling();
        tasks.forEach(task => {
            if (task.status === 'pending' || task.status === 'processing') {
                startPolling(task.id);
            }
        });
    } catch(e) { console.error(e); el.innerHTML = '<div class="empty-history">❌ Ошибка соединения</div>'; }
    finally { isHistLoading = false; }
}

function toggleSelectMode() {
    selectMode = !selectMode; selectedIds.clear();
    const list = document.getElementById('historyList');
    const bar  = document.getElementById('selectionBar');
    const btn  = document.getElementById('btnSelectMode');
    list.classList.toggle('select-mode', selectMode);
    bar.classList.toggle('visible', selectMode);
    btn.classList.toggle('active', selectMode);
    btn.textContent = selectMode ? 'Отмена' : 'Выбрать';
    document.getElementById('btnDeleteAll').style.display = (!selectMode && !!document.querySelector('.history-item')) ? 'block' : 'none';
    document.querySelectorAll('.history-item').forEach(el => el.classList.remove('selected'));
    document.querySelectorAll('.task-checkbox').forEach(cb => { cb.checked = false; });
    updateSelCount();
}
function updateSelCount() {
    document.getElementById('selectionCount').textContent = `Выбрано: ${selectedIds.size}`;
    const total = document.querySelectorAll('.task-checkbox').length;
    document.getElementById('btnSelectAll').textContent = (selectedIds.size===total&&total>0)?'Снять все':'Выбрать все';
}
function toggleSelectAll() {
    const cbs = document.querySelectorAll('.task-checkbox');
    const all = selectedIds.size === cbs.length && cbs.length > 0;
    cbs.forEach(cb => { cb.checked = !all; const id = cb.dataset.id; if(!all){selectedIds.add(id);cb.closest('.history-item').classList.add('selected');}else{selectedIds.delete(id);cb.closest('.history-item').classList.remove('selected');} });
    updateSelCount();
}
function onCbChange(cb) {
    const id = cb.dataset.id; const item = cb.closest('.history-item');
    if (cb.checked) { selectedIds.add(id); item.classList.add('selected'); }
    else            { selectedIds.delete(id); item.classList.remove('selected'); }
    updateSelCount();
}

function removeItemDom(id) {
    const el = document.querySelector(`.history-item[data-task-id="${id}"]`);
    if (!el) return;
    el.classList.add('deleting');
    setTimeout(() => { el.remove(); if (!document.querySelector('.history-item')) { document.getElementById('historyList').innerHTML='<div class="empty-history">📭 История обработок пуста</div>'; document.getElementById('btnDeleteAll').style.display='none'; document.getElementById('statsGrid').style.display='none'; } }, 270);
}

// renderUnavailableBanners показывает баннер для задач, чью обработку не удалось
// выполнить из-за того, что точные эфемериды/часы за наблюдённую дату ещё не
// опубликованы центрами IGS (типично для запросов «день в день»). После
// показа задачи удаляются из БД, чтобы не оставаться в истории как ошибочные.
function renderUnavailableBanners(items, prefix) {
    const cont = document.getElementById('historyBanners');
    if (!cont) return;
    if (!items || items.length === 0) { cont.innerHTML = ''; return; }
    cont.innerHTML = items.map(t => {
        const msg = t.errorMessage.slice(prefix.length).trim();
        const fname = escHtml(t.filename || '—');
        return `<div class="info-banner" data-task-id="${t.id}">
            <div class="info-banner-icon">⏳</div>
            <div class="info-banner-body">
                <div class="info-banner-title">Данные ещё не опубликованы (${fname})</div>
                <div class="info-banner-text">${escHtml(msg)}</div>
            </div>
            <button class="info-banner-close" aria-label="Закрыть" onclick="dismissUnavailableBanner('${t.id}')">×</button>
        </div>`;
    }).join('');
    // Удаляем эти задачи из БД в фоне — они не имеют полезной нагрузки.
    items.forEach(t => {
        fetch(`/api/measurements/delete?id=${t.id}`, {
            method: 'DELETE',
            headers: { 'Authorization': `Bearer ${getToken()}` }
        }).catch(() => {});
    });
}

function dismissUnavailableBanner(taskId) {
    const b = document.querySelector(`#historyBanners .info-banner[data-task-id="${taskId}"]`);
    if (b) b.remove();
}

function confirmDeleteOne(id) {
    openConfirm('Удалить запись?', 'Задача и результат будут удалены.', async () => {
        removeItemDom(id);
        try { await fetch(`/api/measurements/delete?id=${id}`, { method:'DELETE', headers:{ 'Authorization':`Bearer ${getToken()}` } }); showToast('Запись удалена'); }
        catch { showToast('Ошибка', 'err'); loadHistory(); }
    });
}
function confirmDeleteAll() {
    openConfirm('Удалить всю историю?', 'Все задачи и результаты будут удалены.', async () => {
        try { const r = await fetch('/api/measurements/delete-all', { method:'DELETE', headers:{ 'Authorization':`Bearer ${getToken()}` } }); const d = await r.json(); showToast(`Удалено ${d.deleted} записей`); }
        catch { showToast('Ошибка', 'err'); }
        finally { if(selectMode) toggleSelectMode(); loadHistory(); }
    });
}
function confirmDeleteSelected() {
    if (!selectedIds.size) { showToast('Ничего не выбрано', 'err'); return; }
    const n = selectedIds.size;
    openConfirm(`Удалить выбранные (${n})?`, 'Действие необратимо.', async () => {
        const ids = [...selectedIds]; ids.forEach(id => removeItemDom(id)); selectedIds.clear(); updateSelCount();
        const res = await Promise.allSettled(ids.map(id => fetch(`/api/measurements/delete?id=${id}`, { method:'DELETE', headers:{ 'Authorization':`Bearer ${getToken()}` } }).then(r => { if(!r.ok&&r.status!==404) throw new Error(); }) ));
        const errs = res.filter(r=>r.status==='rejected').length;
        if (!errs) showToast(`Удалено ${ids.length} записей`);
        else { showToast(`Удалено: ${ids.length-errs}, ошибок: ${errs}`, 'err'); loadHistory(); }
        if (selectMode) toggleSelectMode();
    });
}

async function downloadResult(taskId, event) {
    event.stopPropagation();
    const btn = event.target; const orig = btn.textContent;
    btn.disabled = true; btn.textContent = '⏳';
    try {
        const r = await fetch(`/api/measurements/download?id=${taskId}`, { headers:{ 'Authorization':`Bearer ${getToken()}` } });
        if (r.ok) {
            const blob = await r.blob(); const url = URL.createObjectURL(blob);
            const a = document.createElement('a'); a.href = url; a.download = `${taskId}.pos`;
            document.body.appendChild(a); a.click(); URL.revokeObjectURL(url); document.body.removeChild(a);
        } else if (r.status===401) { window.location.href='/login'; }
        else { showToast('Файл недоступен', 'err'); }
    } catch { showToast('Ошибка', 'err'); }
    finally { btn.disabled = false; btn.textContent = orig; }
}

/* ════════════════════════════════════════════
   REPORT
════════════════════════════════════════════ */

async function generateReport(taskId, task) {
    const r = task.result;
    if (!r) return;

    // --- координаты ---
    let lat = r.latitude, lon = r.longitude, h = r.height || 0;
    let sN = 0, sE = 0, sU = 0;
    const isKinOrAbs = task.config?.mode === 'kinematic' || task.config?.method === 'single';
    if (r.lastSolutionLine) {
        const f = r.lastSolutionLine.trim().split(/\s+/);
        if (f[2]) lat = parseFloat(f[2]);
        if (f[3]) lon = parseFloat(f[3]);
        if (f[4]) h   = parseFloat(f[4]);
        if (f[7]) sN  = parseFloat(f[7]);
        if (f[8]) sE  = parseFloat(f[8]);
        if (f[9]) sU  = parseFloat(f[9]);
    }
    if (isKinOrAbs && r.sdx > 0 && r.sdy > 0 && r.sdz > 0) {
        sN = r.sdx; sE = r.sdy; sU = r.sdz;
    }
    const xyz  = blhToECEF(lat, lon, h);
    const sXYZ = (sN > 0 && sE > 0 && sU > 0) ? enuSigmaToECEF(lat, lon, sE, sN, sU) : null;

    // --- дата наблюдений ---
    let obsDate = '—';
    try {
        const od = await fetch(`/api/measurements/observation-date?task_id=${taskId}`, {
            headers: { 'Authorization': `Bearer ${getToken()}` }
        });
        if (od.ok) { const d = await od.json(); obsDate = d.date || '—'; }
    } catch {}

    // --- траектория ---
    let trajSvg = '', qTimelineSvg = '', heightSvg = '', qBarSvg = '';
    let trajDebug = '';
    if (isKinOrAbs) {
        try {
            const tr = await fetch(`/api/measurements/trajectory?id=${taskId}`, {
                headers: { 'Authorization': `Bearer ${getToken()}` }
            });
            trajDebug += `HTTP ${tr.status}; `;
            if (tr.ok) {
                const td = await tr.json();
                trajDebug += `points=${td.points?.length ?? 0}; `;
                trajSvg      = buildTrajectorySvg(td);
                qTimelineSvg = buildQTimelineSvg(td.points);
                heightSvg    = buildHeightProfileSvg(td.points);
                qBarSvg      = buildQBarSvg(td.points);
                trajDebug += `svg=${trajSvg ? 'ok' : 'empty'}`;
            } else {
                const txt = await tr.text();
                trajDebug += `error: ${txt}`;
            }
        } catch(e) {
            trajDebug += `exception: ${e.message}`;
        }
    } else {
        trajDebug = `isKinOrAbs=false (mode=${task.config?.mode}, method=${task.config?.method})`;
    }

    // --- тип продуктов по дате ---
    // obsDate из RINEX-заголовка, fallback — дата создания задачи
    const productDate = (obsDate && obsDate !== '—') ? obsDate : task.createdAt;
    const products = getProductType(productDate, task.config?.method);

    // --- рендер ---
    const method = getMethodName(task.config?.method);
    const mode   = isKinOrAbs ? 'Кинематика' : 'Статика';
    const q      = r.q === 1 ? 'FIX' : r.q === 6 ? 'FLOAT' : r.q ? `Q=${r.q}` : '—';
    const fixStr = (r.fixRate != null && isKinOrAbs) ? `${r.fixRate.toFixed(1)} %` : '—';
    const createdStr = new Date(task.createdAt).toLocaleString('ru');
    const fmtDeg = v => Number(v).toFixed(8) + '°';
    const fmtM   = v => Number(v).toFixed(4) + ' м';
    const fmtS   = v => v > 0 ? `σ ${v.toFixed(4)} м` : '—';

    const sigmaRows = sXYZ ? `
        <tr><td>σX</td><td>${fmtS(sXYZ.x)}</td></tr>
        <tr><td>σY</td><td>${fmtS(sXYZ.y)}</td></tr>
        <tr><td>σZ</td><td>${fmtS(sXYZ.z)}</td></tr>` : '';

    const sigmaNote = sXYZ
        ? `<p class="rp-note">σ — стандартное отклонение по внутренней сходимости${isKinOrAbs ? ', среднее по эпохам' : ''}.</p>`
        : '';

    const html = `<!DOCTYPE html><html lang="ru"><head>
<meta charset="UTF-8">
<title>Отчёт об обработке — ${taskId.slice(0,8)}</title>
<style>
  * { box-sizing:border-box; margin:0; padding:0; }
  body { font-family:'Segoe UI',Arial,sans-serif; font-size:11pt; color:#111; background:#fff; padding:20mm 18mm; }
  h1 { font-size:17pt; font-weight:300; letter-spacing:-0.01em; margin-bottom:4px; }
  h2 { font-size:10pt; font-weight:700; text-transform:uppercase; letter-spacing:0.12em; color:#555;
       margin:20px 0 8px; border-bottom:1px solid #ddd; padding-bottom:4px; }
  .meta { font-size:9pt; color:#777; margin-bottom:18px; font-family:monospace; }
  table { width:100%; border-collapse:collapse; margin-bottom:6px; }
  td { padding:4px 8px; font-size:10pt; vertical-align:top; }
  td:first-child { width:44%; color:#555; font-size:9.5pt; }
  tr:nth-child(odd) td { background:#f8f8f8; }
  .mono { font-family:'Courier New',monospace; font-size:10pt; }
  .rp-note { font-size:8.5pt; color:#888; margin-top:6px; font-style:italic; }
  .traj-wrap { margin:10px 0; text-align:center; }
  .traj-wrap svg { max-width:100%; border:1px solid #e0e0e0; border-radius:4px; }
  .legend { display:flex; gap:16px; justify-content:center; margin-top:6px; font-size:8.5pt; color:#555; }
  .legend span { display:inline-flex; align-items:center; gap:4px; }
  .leg-dot { width:9px; height:9px; border-radius:50%; display:inline-block; }
  .footer { margin-top:28px; border-top:1px solid #ddd; padding-top:8px;
            font-size:8pt; color:#aaa; text-align:right; font-family:monospace; }
  @media print { body { padding:15mm 12mm; } }
</style></head><body>
<h1>Отчёт об обработке ГНСС-наблюдений</h1>
<div class="meta">ID задачи: ${taskId} &nbsp;·&nbsp; Сформирован: ${new Date().toLocaleString('ru')}</div>

<h2>Общая информация</h2>
<table>
  <tr><td>Файл наблюдений</td><td class="mono">${escHtml(task.filename || '—')}</td></tr>
  <tr><td>Дата наблюдений</td><td class="mono">${obsDate}</td></tr>
  <tr><td>Дата обработки</td><td>${createdStr}</td></tr>
  <tr><td>Метод</td><td>${method}</td></tr>
  <tr><td>Режим</td><td>${mode}</td></tr>
</table>

<h2>Результат позиционирования</h2>
<table>
  <tr><td>Система координат</td><td>ITRF2020</td></tr>
  <tr><td>Эпоха</td><td class="mono">${obsDate}</td></tr>
  <tr><td>B (широта)</td><td class="mono">${fmtDeg(lat)}</td></tr>
  <tr><td>L (долгота)</td><td class="mono">${fmtDeg(lon)}</td></tr>
  <tr><td>H (высота)</td><td class="mono">${fmtM(h)}</td></tr>
  <tr><td>X</td><td class="mono">${fmtM(xyz.x)}</td></tr>
  <tr><td>Y</td><td class="mono">${fmtM(xyz.y)}</td></tr>
  <tr><td>Z</td><td class="mono">${fmtM(xyz.z)}</td></tr>
  ${sigmaRows}
</table>
${sigmaNote}

<h2>Качество решения</h2>
<table>
  <tr><td>Тип решения</td><td>${q}</td></tr>
  ${isKinOrAbs ? `<tr><td>Доля FIX-эпох</td><td>${fixStr}</td></tr>` : ''}
  <tr><td>Макс. кол-во спутников</td><td>${r.nSat || '—'}</td></tr>
</table>

${products.sp3 ? `
<h2>Использованные продукты</h2>
<table>
  <tr><td>Эфемериды SP3</td><td>${products.sp3}</td></tr>
  <tr><td>Часы CLK</td><td>${products.clk}</td></tr>
  <tr><td>DCB / OSB</td><td>${products.dcb}</td></tr>
  <tr><td>ERP</td><td>${products.erp}</td></tr>
</table>` : ''}

${isKinOrAbs ? `
<h2>Траектория</h2>
${trajSvg
    ? `<div class="traj-wrap">${trajSvg}</div>
       <div class="legend">
         <span><span class="leg-dot" style="background:#22c55e"></span>FIX</span>
         <span><span class="leg-dot" style="background:#f59e0b"></span>FLOAT</span>
       </div>`
    : `<p style="color:#999;font-size:9pt;font-family:monospace">Траектория недоступна: ${trajDebug}</p>`
}` : ''}

${(qTimelineSvg || heightSvg) ? `
<h2>Статистика обработки</h2>
${qTimelineSvg ? `
<p style="font-size:9pt;color:#555;margin-bottom:4px;">Качество решения по эпохам</p>
<div style="margin-bottom:14px;">${qTimelineSvg}</div>` : ''}
<div style="display:flex;gap:20px;align-items:flex-start;flex-wrap:wrap;">
${heightSvg ? `<div><p style="font-size:9pt;color:#555;margin-bottom:4px;">Профиль высоты, м</p>${heightSvg}</div>` : ''}
${qBarSvg   ? `<div><p style="font-size:9pt;color:#555;margin-bottom:4px;">Распределение эпох</p>${qBarSvg}</div>` : ''}
</div>` : ''}

<div class="footer">CPS · ${window.location.hostname} · ${new Date().toLocaleDateString('ru')}</div>
<script>window.onload = () => window.print();<\/script>
</body></html>`;

    const w = window.open('', '_blank');
    w.document.write(html);
    w.document.close();
}

// Тип продуктов IGS по дате наблюдений (final доступен через ~2 нед, rapid ~1 сут)
function getProductType(obsDateStr, method) {
    if (!method || method.toLowerCase() !== 'ppp') return {};
    if (!obsDateStr || obsDateStr === '—') return { sp3:'—', clk:'—', dcb:'—', erp:'—' };
    const days = (new Date() - new Date(obsDateStr)) / 86400000;
    const type = days > 14 ? 'Final (IGS/CODE)' : days > 1 ? 'Rapid (IGS/CODE)' : 'Ultra-rapid (IGS)';
    return { sp3: type, clk: type, dcb: days > 14 ? 'Final (CODE MGEX)' : 'Rapid (CAS/CODE)', erp: type };
}

// Строит SVG траектории из данных /api/measurements/trajectory
function buildTrajectorySvg(td) {
    const pts = td.points;
    if (!pts || pts.length === 0) return '';

    const W = 700, H = 340, PAD = 28;
    const dLat = td.maxLat - td.minLat || 0.0001;
    const dLon = td.maxLon - td.minLon || 0.0001;
    const scale = Math.min((W - PAD*2) / dLon, (H - PAD*2) / dLat);
    const offX  = (W - dLon * scale) / 2;
    const offY  = (H - dLat * scale) / 2;

    const px = p => offX + (p.lon - td.minLon) * scale;
    const py = p => H - offY - (p.lat - td.minLat) * scale;
    const qColor = q => q === 1 ? '#22c55e' : '#f59e0b';

    let path = `M${px(pts[0]).toFixed(1)},${py(pts[0]).toFixed(1)}`;
    for (let i = 1; i < pts.length; i++) path += ` L${px(pts[i]).toFixed(1)},${py(pts[i]).toFixed(1)}`;

    const step = Math.max(1, Math.floor(pts.length / 1200));
    let circles = '';
    for (let i = 0; i < pts.length; i += step) {
        const p = pts[i];
        circles += `<circle cx="${px(p).toFixed(1)}" cy="${py(p).toFixed(1)}" r="2.5" fill="${qColor(p.q)}"/>`;
    }

    let arrow = '';
    if (pts.length >= 2) {
        const a = pts[pts.length - 2], b = pts[pts.length - 1];
        const ax = px(a), ay = py(a), bx = px(b), by = py(b);
        const ang = Math.atan2(by - ay, bx - ax);
        const len = 12;
        const x1 = bx - len * Math.cos(ang - 0.4);
        const y1 = by - len * Math.sin(ang - 0.4);
        const x2 = bx - len * Math.cos(ang + 0.4);
        const y2 = by - len * Math.sin(ang + 0.4);
        arrow = `<polyline points="${x1.toFixed(1)},${y1.toFixed(1)} ${bx.toFixed(1)},${by.toFixed(1)} ${x2.toFixed(1)},${y2.toFixed(1)}"
            fill="none" stroke="#333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`;
    }

    const mLat = (dLat * 111320).toFixed(0);
    const mLon = (dLon * 111320 * Math.cos(((td.minLat+td.maxLat)/2) * Math.PI/180)).toFixed(0);
    const scaleBar = `<text x="${PAD}" y="${H - 8}" font-size="10" fill="#999" font-family="monospace">
        ΔB=${mLat} м  ΔL=${mLon} м  n=${pts.length} эп.</text>`;

    return `<svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" xmlns="http://www.w3.org/2000/svg">
        <rect width="${W}" height="${H}" fill="#fafafa"/>
        <path d="${path}" fill="none" stroke="#e0e0e0" stroke-width="1"/>
        ${circles}
        ${arrow}
        ${scaleBar}
    </svg>`;
}

// ── Временна́я шкала качества (Q-timeline) ─────────────────────────────────
function buildQTimelineSvg(pts) {
    if (!pts || pts.length === 0) return '';
    const W = 520, H = 32, PAD = 0;
    const step = Math.max(1, Math.floor(pts.length / W));
    const bars = [];
    for (let i = 0; i < pts.length; i += step) {
        const x = Math.round((i / pts.length) * W);
        const w = Math.max(1, Math.round((step / pts.length) * W));
        const col = pts[i].q === 1 ? '#22c55e' : '#f59e0b';
        bars.push(`<rect x="${x}" y="0" width="${w}" height="${H}" fill="${col}"/>`);
    }
    const nFix = pts.filter(p => p.q === 1).length;
    const pFix = ((nFix / pts.length) * 100).toFixed(1);
    return `<svg width="${W}" height="${H + 22}" viewBox="0 0 ${W} ${H + 22}" xmlns="http://www.w3.org/2000/svg">
        <rect width="${W}" height="${H}" fill="#f3f4f6" rx="3"/>
        ${bars.join('')}
        <rect width="${W}" height="${H}" fill="none" stroke="#ddd" stroke-width="0.5" rx="3"/>
        <text x="6"   y="${H + 16}" font-size="11" fill="#22c55e" font-family="monospace" font-weight="bold">FIX ${pFix}%</text>
        <text x="110" y="${H + 16}" font-size="11" fill="#f59e0b" font-family="monospace" font-weight="bold">FLOAT ${(100 - parseFloat(pFix)).toFixed(1)}%</text>
        <text x="${W - 6}" y="${H + 16}" font-size="11" fill="#999" font-family="monospace" text-anchor="end">${pts.length} эпох</text>
    </svg>`;
}

// ── Профиль высоты по эпохам ────────────────────────────────────────────────
function buildHeightProfileSvg(pts) {
    if (!pts || pts.length === 0) return '';
    const W = 700, H = 160, PAD_L = 68, PAD_B = 28, PAD_T = 12;
    const innerW = W - PAD_L - 12, innerH = H - PAD_B - PAD_T;

    const heights = pts.map(p => p.h);
    const hMin = Math.min(...heights), hMax = Math.max(...heights);
    const hRange = hMax - hMin || 0.001;

    const step = Math.max(1, Math.floor(pts.length / 600));
    const px = i => PAD_L + (i / (pts.length - 1)) * innerW;
    const py = h => PAD_T + innerH - ((h - hMin) / hRange) * innerH;

    // Линия высоты с цветом по Q
    let pathSegs = '';
    for (let i = 0; i < pts.length - 1; i += step) {
        const p1 = pts[i], p2 = pts[Math.min(i + step, pts.length - 1)];
        const col = p1.q === 1 ? '#22c55e' : '#f59e0b';
        pathSegs += `<line x1="${px(i).toFixed(1)}" y1="${py(p1.h).toFixed(1)}" x2="${px(i+step).toFixed(1)}" y2="${py(p2.h).toFixed(1)}" stroke="${col}" stroke-width="1.5"/>`;
    }

    // Ось Y — засечки
    const nTicks = 5;
    let yAxis = '';
    for (let t = 0; t <= nTicks; t++) {
        const hVal = hMin + (t / nTicks) * hRange;
        const yPos = py(hVal);
        yAxis += `<line x1="${PAD_L}" y1="${yPos.toFixed(1)}" x2="${W - 12}" y2="${yPos.toFixed(1)}" stroke="#e5e7eb" stroke-width="0.8"/>`;
        yAxis += `<text x="${PAD_L - 6}" y="${(yPos + 4).toFixed(1)}" font-size="10" fill="#888" font-family="monospace" text-anchor="end">${hVal.toFixed(3)}</text>`;
    }

    // Ось X — засечки по эпохам
    let xAxis = '';
    for (let t = 0; t <= 4; t++) {
        const idx = Math.round((t / 4) * (pts.length - 1));
        const xPos = px(idx);
        xAxis += `<line x1="${xPos.toFixed(1)}" y1="${PAD_T + innerH}" x2="${xPos.toFixed(1)}" y2="${PAD_T + innerH + 4}" stroke="#ccc" stroke-width="0.8"/>`;
        xAxis += `<text x="${xPos.toFixed(1)}" y="${H - 6}" font-size="10" fill="#aaa" font-family="monospace" text-anchor="middle">${idx}</text>`;
    }

    return `<svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" xmlns="http://www.w3.org/2000/svg">
        <rect width="${W}" height="${H}" fill="#fafafa"/>
        ${yAxis}
        <line x1="${PAD_L}" y1="${PAD_T}" x2="${PAD_L}" y2="${PAD_T + innerH}" stroke="#ccc" stroke-width="1"/>
        <line x1="${PAD_L}" y1="${PAD_T + innerH}" x2="${W - 12}" y2="${PAD_T + innerH}" stroke="#ccc" stroke-width="1"/>
        ${pathSegs}
        ${xAxis}
        <text x="${PAD_L / 2 - 4}" y="${PAD_T + innerH / 2}" font-size="10" fill="#aaa" font-family="monospace"
              text-anchor="middle" transform="rotate(-90 ${PAD_L / 2 - 4} ${PAD_T + innerH / 2})">Высота, м</text>
        <text x="${PAD_L + innerW / 2}" y="${H}" font-size="10" fill="#aaa" font-family="monospace" text-anchor="middle">Эпоха</text>
    </svg>`;
}

// ── Столбчатая диаграмма по сессиям ────────────────────────────────────────
function buildQBarSvg(pts) {
    if (!pts || pts.length === 0) return '';
    const nFix   = pts.filter(p => p.q === 1).length;
    const nFloat = pts.length - nFix;
    const total  = pts.length;
    const W = 260, H = 160, BAR_W = 72, GAP = 32, PAD_L = 52, PAD_B = 32, PAD_T = 12;
    const innerH = H - PAD_B - PAD_T;

    const maxVal = Math.max(nFix, nFloat, 1);
    const hFix   = (nFix   / maxVal) * innerH;
    const hFloat = (nFloat / maxVal) * innerH;

    const x1 = PAD_L, x2 = PAD_L + BAR_W + GAP;
    const yFix   = PAD_T + innerH - hFix;
    const yFloat = PAD_T + innerH - hFloat;

    const pFix   = ((nFix   / total) * 100).toFixed(1);
    const pFloat = ((nFloat / total) * 100).toFixed(1);

    // Засечки оси Y
    let yAxis = '';
    for (let t = 0; t <= 4; t++) {
        const y = PAD_T + innerH - (t / 4) * innerH;
        yAxis += `<line x1="${PAD_L}" y1="${y.toFixed(1)}" x2="${W - 8}" y2="${y.toFixed(1)}" stroke="#e5e7eb" stroke-width="0.8"/>`;
        const val = Math.round((t / 4) * maxVal);
        yAxis += `<text x="${PAD_L - 6}" y="${(y + 4).toFixed(1)}" font-size="10" fill="#999" font-family="monospace" text-anchor="end">${val}</text>`;
    }

    // Подписи над столбцами
    const labelFix   = `<text x="${(x1 + BAR_W/2).toFixed(1)}" y="${(yFix - 5).toFixed(1)}"   font-size="11" fill="#22c55e" font-family="monospace" font-weight="bold" text-anchor="middle">${nFix}</text>`;
    const labelFloat = `<text x="${(x2 + BAR_W/2).toFixed(1)}" y="${(yFloat - 5).toFixed(1)}" font-size="11" fill="#f59e0b" font-family="monospace" font-weight="bold" text-anchor="middle">${nFloat}</text>`;

    return `<svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" xmlns="http://www.w3.org/2000/svg">
        <rect width="${W}" height="${H}" fill="#fafafa"/>
        ${yAxis}
        <line x1="${PAD_L}" y1="${PAD_T}" x2="${PAD_L}" y2="${PAD_T + innerH}" stroke="#ccc" stroke-width="1"/>
        <line x1="${PAD_L}" y1="${PAD_T + innerH}" x2="${W - 8}" y2="${PAD_T + innerH}" stroke="#ccc" stroke-width="1"/>
        <rect x="${x1}" y="${yFix.toFixed(1)}"   width="${BAR_W}" height="${Math.max(hFix,1).toFixed(1)}"   fill="#22c55e" rx="3"/>
        <rect x="${x2}" y="${yFloat.toFixed(1)}" width="${BAR_W}" height="${Math.max(hFloat,1).toFixed(1)}" fill="#f59e0b" rx="3"/>
        ${labelFix}${labelFloat}
        <text x="${(x1 + BAR_W/2).toFixed(1)}" y="${H - 8}" font-size="11" fill="#22c55e" font-family="monospace" text-anchor="middle" font-weight="bold">FIX</text>
        <text x="${(x2 + BAR_W/2).toFixed(1)}" y="${H - 8}" font-size="11" fill="#f59e0b" font-family="monospace" text-anchor="middle" font-weight="bold">FLOAT</text>
        <text x="${(x1 + BAR_W/2).toFixed(1)}" y="${H - 18}" font-size="9.5" fill="#555" font-family="monospace" text-anchor="middle">${pFix}%</text>
        <text x="${(x2 + BAR_W/2).toFixed(1)}" y="${H - 18}" font-size="9.5" fill="#555" font-family="monospace" text-anchor="middle">${pFloat}%</text>
    </svg>`;
}

/* ════════════════════════════════════════════
   TRANSFORM
════════════════════════════════════════════ */
function updateEpochVisibility() {
    const src = document.getElementById('sourceCRS').value;
    const tgt = document.getElementById('targetCRS').value;
    document.getElementById('sourceEpochRow').style.display = STATIC_CRS.has(src) ? 'none' : '';
    document.getElementById('targetEpochRow').style.display = STATIC_CRS.has(tgt) ? 'none' : '';
}
document.getElementById('sourceCRS').addEventListener('change', updateEpochVisibility);
document.getElementById('targetCRS').addEventListener('change', updateEpochVisibility);

async function openTransform(lat, lon, height, taskId) {
    currentTransformCoords = { lat, lon, height, taskId };
    const info = document.getElementById('sourceCoordsInfo');
    info.innerHTML = `<strong>Исходные координаты:</strong><br><span style="font-family:'JetBrains Mono',monospace;font-size:0.82rem;">B = ${formatCoord(lat)}°<br>L = ${formatCoord(lon)}°<br>${height?`h = ${formatH(height)} м`:''}</span><div id="obsDateInfo" style="margin-top:6px;font-size:0.75rem;color:var(--muted);">⏳ Загрузка даты…</div>`;
    document.getElementById('sourceCRS').value = 'ITRF2020';
    document.querySelector('input[name="sourceCoordType"][value="BLH"]').checked = true;
    document.getElementById('targetCRS').value = 'ГСК-2011';
    document.querySelector('input[name="targetCoordType"][value="BLH"]').checked = true;
    document.getElementById('targetEpoch').value = new Date().toISOString().slice(0,10);
    updateEpochVisibility();
    openModal('transformModal');
    document.getElementById('transformResult').classList.remove('visible');
    try {
        const r = await fetch(`/api/measurements/observation-date?task_id=${taskId}`, { headers:{ 'Authorization':`Bearer ${getToken()}` } });
        if (r.ok) {
            const d = await r.json();
            document.getElementById('sourceEpoch').value = d.date;
            document.getElementById('obsDateInfo').innerHTML = `<span style="color:var(--ok);display:inline-flex;align-items:center;gap:4px;">${icon('check',14)} Дата наблюдения: ${new Date(d.date).toLocaleDateString('ru')}</span>`;
        } else { document.getElementById('obsDateInfo').innerHTML = `<span style="display:inline-flex;align-items:center;gap:4px;color:var(--warn);">${icon('warn',14)} Не удалось определить дату</span>`; document.getElementById('sourceEpoch').value = new Date().toISOString().slice(0,10); }
    } catch { document.getElementById('obsDateInfo').textContent = ''; }
}
function swapSystems() {
    const s = document.getElementById('sourceCRS'), t = document.getElementById('targetCRS');
    const st = document.querySelector('input[name="sourceCoordType"]:checked').value;
    const tt = document.querySelector('input[name="targetCoordType"]:checked').value;
    const se = document.getElementById('sourceEpoch').value, te = document.getElementById('targetEpoch').value;
    [s.value, t.value] = [t.value, s.value];
    document.querySelector(`input[name="sourceCoordType"][value="${tt}"]`).checked = true;
    document.querySelector(`input[name="targetCoordType"][value="${st}"]`).checked = true;
    document.getElementById('sourceEpoch').value = te;
    document.getElementById('targetEpoch').value = se;
    updateEpochVisibility();
}
async function performTransform() {
    if (!currentTransformCoords) return;
    const btn = document.getElementById('btnDoTransform');
    btn.disabled = true; btn.textContent = '⏳…';
    try {
        const sourceCRS     = document.getElementById('sourceCRS').value;
        const targetCRS     = document.getElementById('targetCRS').value;
        const sourceType    = document.querySelector('input[name="sourceCoordType"]:checked').value;
        const targetType    = document.querySelector('input[name="targetCoordType"]:checked').value;
        const heightSurface = document.getElementById('heightSurface').value;
        const sourceEpoch   = document.getElementById('sourceEpoch').value;
        const targetEpoch   = document.getElementById('targetEpoch').value;
        const coords = [currentTransformCoords.lon, currentTransformCoords.lat];
        if (currentTransformCoords.height) coords.push(currentTransformCoords.height);
        const geojson = { type:'FeatureCollection', features:[{ type:'Feature', geometry:{ type:'Point', coordinates: coords }, properties:{ id:'1' } }] };
        const params = new URLSearchParams({ source_crs:sourceCRS, target_crs:targetCRS, source_coord_type:sourceType, target_coord_type:targetType, height_surface:heightSurface, source_epoch:sourceEpoch, target_epoch:targetEpoch });
        const r = await fetch(`/api/transform/geojson?${params}`, { method:'POST', headers:{ 'Content-Type':'application/json', 'Authorization':`Bearer ${getToken()}` }, body: JSON.stringify(geojson) });
        if (!r.ok) throw new Error((await r.json().catch(()=>({}))).error || `HTTP ${r.status}`);
        const result = await r.json();
        result._sourceCRS   = sourceCRS; result._targetCRS   = targetCRS;
        result._sourceEpoch = result.source_epoch || sourceEpoch;
        result._targetEpoch = result.target_epoch || targetEpoch;
        displayTransformResult(result, targetType);
    } catch(e) { showToast('Ошибка пересчёта: ' + e.message, 'err'); }
    finally { btn.disabled = false; btn.textContent = '🔄 Пересчитать'; }
}
function displayTransformResult(result, targetType) {
    const div = document.getElementById('transformedCoords');
    const res = document.getElementById('transformResult');
    try {
        let coords = result.target_coordinates;
        if (!coords || coords.length < 2) { const ds = result.full_response?.target_dataset; if (ds) { const p = typeof ds==='string'?JSON.parse(ds):ds; coords = p?.features?.[0]?.geometry?.coordinates; } }
        if (coords && coords.length >= 2) {
            const srcCRS = result._sourceCRS||'—'; const tgtCRS = result._targetCRS||'—';
            const srcEp  = result._sourceEpoch||'—'; const tgtEp  = result._targetEpoch||'—';
            const opCode = result.operation_code||'—';
            const srcL   = STATIC_CRS.has(srcCRS)?'(статическая)':`(${srcEp})`;
            const tgtL   = STATIC_CRS.has(tgtCRS)?'(статическая)':`(${tgtEp})`;
            const fmt10  = v => Number(v).toFixed(10);
            const fmt4   = v => Number(v).toFixed(4);
            const meta   = `<div style="font-size:0.7rem;color:var(--ink-3);margin-bottom:10px;padding:8px;background:var(--paper-2);border:1px solid var(--paper-edge);border-radius:6px;line-height:1.6;"><div><strong>${srcCRS}</strong> ${srcL} → <strong>${tgtCRS}</strong> ${tgtL}</div><div style="font-family:'JetBrains Mono',monospace;font-size:0.62rem;color:#93c5fd;word-break:break-all;">${opCode}</div></div>`;
            let html = meta;
            if (targetType === 'BLH') {
                html += `<div style="font-family:'JetBrains Mono',monospace;font-size:0.82rem;line-height:1.8;">B = ${fmt10(coords[1])}°<br>L = ${fmt10(coords[0])}°<br>${coords[2]?`H = ${fmt4(coords[2])} м`:''}</div>`;
            } else {
                html += `<div style="font-family:'JetBrains Mono',monospace;font-size:0.82rem;line-height:1.8;">X = ${fmt4(coords[0])} м<br>Y = ${fmt4(coords[1])} м<br>${coords[2]?`Z = ${fmt4(coords[2])} м`:''}</div>`;
            }
            const copyText = targetType==='BLH' ? `${fmt10(coords[1])}, ${fmt10(coords[0])}, ${coords[2]?fmt4(coords[2]):''}` : coords.map(c=>fmt4(c)).join(', ');
            html += `<button class="btn-sm btn-sm-ghost" style="margin-top:10px;" onclick="copyClip('${copyText}')">📋 Копировать</button>`;
            div.innerHTML = html;
            res.classList.add('visible');
            showToast('Пересчёт выполнен');
        } else { div.innerHTML = '<div style="color:var(--err);">Не удалось получить координаты</div>'; res.classList.add('visible'); }
    } catch(e) { div.innerHTML = '<div style="color:var(--err);">Ошибка разбора результата</div>'; res.classList.add('visible'); }
}
function copyClip(text) {
    navigator.clipboard?.writeText(text).then(() => showToast('Скопировано')).catch(() => {
        const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); document.body.removeChild(ta); showToast('Скопировано');
    });
}

/* ════════════════════════════════════════════
   INIT
════════════════════════════════════════════ */
if (checkAuth()) {
    loadProfile();
    loadHistory();
}
/* ════════════════════════════════════════════
   DELETE ACCOUNT
════════════════════════════════════════════ */
function openDeleteAccount() {
    document.getElementById('deleteAccountPassword').value = '';
    document.getElementById('deleteAccountErr').textContent = '';
    openModal('deleteAccountModal');
    setTimeout(() => document.getElementById('deleteAccountPassword').focus(), 100);
}

async function confirmDeleteAccount() {
    const password = document.getElementById('deleteAccountPassword').value;
    const errEl    = document.getElementById('deleteAccountErr');
    const btn      = document.getElementById('btnDeleteAccount');

    errEl.textContent = '';
    if (!password) { errEl.textContent = 'Введите пароль'; return; }

    btn.disabled = true;
    btn.textContent = 'Удаление…';

    try {
        const r = await fetch('/api/account', {
            method: 'DELETE',
            headers: { 'Authorization': `Bearer ${getToken()}`, 'Content-Type': 'application/json' },
            body: JSON.stringify({ password })
        });
        const data = await r.json().catch(() => ({}));
        if (!r.ok) {
            errEl.textContent = data.error || 'Неверный пароль';
            btn.disabled = false;
            btn.textContent = 'Удалить навсегда';
            return;
        }
        localStorage.removeItem('token');
        localStorage.removeItem('userLogin');
        window.location.href = '/?deleted=1';
    } catch {
        errEl.textContent = 'Ошибка сети';
        btn.disabled = false;
        btn.textContent = 'Удалить навсегда';
    }
}