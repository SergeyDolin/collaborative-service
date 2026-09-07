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
let historyOffset = 0, historyReload = false;
const pollingBusy = new Set();
const comparisonTasks = new Map();

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
    if (pollingBusy.has(taskId) || document.hidden) return;
    pollingBusy.add(taskId);
    try {
        const r = await fetch(`/api/measurements/status?id=${taskId}`, {
            headers: { 'Authorization': `Bearer ${getToken()}` }
        });
        if (!r.ok) {
            if (r.status === 401 || r.status === 403 || r.status === 404) {
                stopPolling(taskId);
                setTaskConnectionHint(taskId, r.status === 404 ? 'Задача больше недоступна.' : 'Войдите заново для обновления статуса.');
                return;
            }
            throw new Error('status unavailable');
        }
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
            if (status === 'failed') {
                const notice = document.getElementById('taskNotice');
                notice.hidden = false;
                notice.textContent = 'Обработка не завершена: ' + (data.errorMessage || 'Не удалось получить решение') + '. Вы можете выбрать файл и повторить запуск.';
            }
            // Полностью перерисовываем историю чтобы показать результат
            loadHistory();
        } else {
            // Обновляем label прогресс-бара
            const label = item.querySelector('.task-progress-label');
            if (label) label.textContent = getProgressLabel(status, data.processingSec, data.stage)
                + (data.startedAt ? ' · Начало: ' + new Date(data.startedAt).toLocaleTimeString('ru-RU') : '')
                + ' · Проверено: ' + new Date(data.checkedAt || Date.now()).toLocaleTimeString('ru-RU');
        }
    } catch {
        setTaskConnectionHint(taskId, 'Связь с сервером потеряна. Повторяем проверку…');
    } finally { pollingBusy.delete(taskId); }
}

function setTaskConnectionHint(taskId, text) {
    const label = document.querySelector(`.history-item[data-task-id="${taskId}"] .task-progress-label`);
    if (label) label.textContent = text;
}

function getProgressLabel(status, sec, stage) {
    if (status === 'pending')    return 'В очереди…';
    if (status === 'processing') return (Workflow.stages[stage] || 'Обработка') + (sec ? ` · ${sec.toFixed(0)} с` : '…');
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
    if (isHistLoading) { historyReload = true; return; } isHistLoading = true;
    stopAllPolling();
    const el = document.getElementById('historyList');
    el.innerHTML = '<div class="loading-hist">⏳ Загрузка…</div>';
    try {
        const params = new URLSearchParams(new FormData(document.getElementById('historyFilters')));
        params.set('limit', '50'); params.set('offset', String(historyOffset));
        const r = await fetch('/api/measurements/history?' + params, {
            headers: { 'Authorization': `Bearer ${getToken()}` }
        });
        if (!r.ok) {
            el.innerHTML = r.status === 401 ? '<div class="empty-history">🔒 Сессия истекла</div>' : '<div class="empty-history">❌ Ошибка загрузки</div>';
            return;
        }
        let tasks = await r.json();
        document.getElementById('historyPrev').disabled = historyOffset === 0;
        document.getElementById('historyNext').disabled = !tasks || tasks.length < 50;
        document.getElementById('historyPage').textContent = 'Страница ' + (historyOffset/50+1);
        selectedIds.clear(); updateSelCount();
        if (!tasks || tasks.length === 0) {
            el.innerHTML = '<div class="empty-history">Нет доступных записей для выбранных фильтров</div>';
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
            el.innerHTML = '<div class="empty-history">Нет доступных записей для выбранных фильтров</div>';
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
                const hasCoords = r.q > 0 && Number.isFinite(r.latitude) && Number.isFinite(r.longitude);
                const fixRate = Number.isFinite(r.fixRate) ? r.fixRate.toFixed(1) : null;
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
                if (hasCoords && Number.isFinite(lat) && Number.isFinite(lon)) {
                    coordsHtml = buildCoordsHtml(lat, lon, h, sN, sE, sU, isKinOrAbs);
                }
                const dlBtn = task.fileType !== 'static' && task.hasResultFile
                    ? `<button class="download-btn" onclick="downloadResult('${task.id}',event)"><span data-icon="download" data-icon-size="14"></span> Скачать .pos</button>` : '';
                const trBtn = hasCoords
                    ? `<button class="btn-transform" onclick="openTransform(${r.latitude},${r.longitude},${r.height||0},'${task.id}')"><span data-icon="refresh" data-icon-size="14"></span> Пересчёт</button>` : '';
                const repBtn = `<button class="btn-report" onclick="generateReport('${task.id}', _histTasks['${task.id}'])"><span data-icon="file" data-icon-size="14"></span> Отчёт</button>`;
                resultHtml = `<div class="result-block">
                    <div class="stats-info">${getSolutionStatus(r.q)}${fixRate?` <span>(${fixRate}%)</span>`:''} ${r.nSat?`<span><span data-icon="satellite" data-icon-size="12"></span> ${r.nSat}</span>`:''}</div>
                    ${coordsHtml}
                    <p class="workflow-note">B, L — градусы; H — высота над эллипсоидом. Реализация системы координат и эпоха требуют проверки по исходным продуктам перед пересчётом.</p>
                    <div class="action-buttons">${dlBtn}${trBtn}${repBtn}</div>
                    <div class="action-buttons">
                     ${hasCoords ? `<button class="btn-report" onclick="copyResultCoords('${task.id}')">Скопировать B, L, H</button><button class="btn-report" onclick="compareResult('${task.id}')">Сравнить</button>` : ''}
                     <button class="btn-report" onclick="repeatSettings('${task.id}')">Повторить с этими настройками</button>
                    </div>
                    <p class="workflow-note">Доступен до ${escHtml(new Date(task.resultExpiresAt).toLocaleString('ru-RU'))}. ${task.hasResultFile ? 'Файл удаляется с сервера после скачивания.' : 'Файл уже недоступен для скачивания.'}</p>
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
        if (location.hash.startsWith('#task-')) {
            const target = document.querySelector('.history-item[data-task-id="' + CSS.escape(decodeURIComponent(location.hash.slice(6))) + '"]');
            if (target) { target.scrollIntoView({block:'center', behavior:'smooth'}); history.replaceState(null, '', location.pathname); }
        }
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
    finally { isHistLoading = false; if (historyReload) { historyReload = false; loadHistory(); } }
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
    if (comparisonTasks.has(id)) {
        comparisonTasks.clear();
        document.getElementById('comparisonPanel').hidden = true;
        document.getElementById('comparisonPanel').textContent = '';
    }
    const el = document.querySelector(`.history-item[data-task-id="${id}"]`);
    if (!el) return;
    el.classList.add('deleting');
    setTimeout(() => { el.remove(); if (!document.querySelector('.history-item')) { document.getElementById('historyList').innerHTML='<div class="empty-history">Нет доступных записей для выбранных фильтров</div>'; document.getElementById('btnDeleteAll').style.display='none'; document.getElementById('statsGrid').style.display='none'; } }, 270);
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
    const btn = event.currentTarget; const orig = btn.innerHTML;
    btn.disabled = true; btn.textContent = '⏳';
    try {
        const r = await fetch(`/api/measurements/download?id=${taskId}`, { headers:{ 'Authorization':`Bearer ${getToken()}` } });
        if (r.ok) {
            const blob = await r.blob(); const url = URL.createObjectURL(blob);
            const a = document.createElement('a'); a.href = url; a.download = r.headers.get('Content-Disposition')?.match(/filename=([^;]+)/)?.[1]?.replace(/^"|"$/g, '') || `${taskId}.pos`;
            document.body.appendChild(a); a.click(); URL.revokeObjectURL(url); document.body.removeChild(a);
            const t = window._histTasks?.[taskId]; if (t) t.hasResultFile = false;
            btn.remove();
            showToast('Файл скачан и удалён с сервера');
        } else if (r.status===401) { window.location.href='/login'; }
        else { showToast('Файл недоступен', 'err'); }
    } catch { showToast('Ошибка', 'err'); }
    finally { btn.disabled = false; btn.innerHTML = orig; }
}

/* ════════════════════════════════════════════
   REPORT
════════════════════════════════════════════ */

async function generateReport(taskId, task) {
    if (!task?.result) { showToast('Результат недоступен. Обновите список обработок.', 'err'); return; }
    const w = window.open('', '_blank');
    if (!w) { showToast('Разрешите открытие новой вкладки для отчёта', 'err'); return; }
    w.document.write('<!doctype html><meta charset="utf-8"><p>Подготовка отчёта…</p>');
    w.document.close();
    try {
        if (typeof GNSSReport === 'undefined' || typeof GNSSReport.render !== 'function') {
            throw new Error('Не загружен модуль отчёта report.js. Обновите файлы static/js/report.js и static/profile.html на сервере, затем перезагрузите страницу профиля.');
        }
        let data = null, unavailable = '';
        const controller = new AbortController();
        const timer = setTimeout(() => controller.abort(), 30000);
        try {
            const response = await fetch(`/api/measurements/trajectory?id=${encodeURIComponent(taskId)}`, {
                headers: {Authorization: `Bearer ${getToken()}`}, cache: 'no-store', signal: controller.signal
            });
            if (response.ok) data = await response.json();
            else if (response.status === 401) unavailable = 'Сессия истекла. Войдите заново для получения статистики.';
            else if (response.status === 404) unavailable = 'Данные эпох недоступны или уже удалены.';
            else unavailable = 'Не удалось получить данные эпох. Повторите формирование отчёта позже.';
        } catch { unavailable = 'Не удалось загрузить данные эпох: проверьте соединение и повторите попытку.'; }
        finally { clearTimeout(timer); }
        if (!w.closed) {
            // Keep the loading page intact if report generation throws.
            const html = GNSSReport.render(task, data, unavailable);
            w.document.open();
            w.document.write(html);
            w.document.close();
        }
    } catch (e) {
        if (!w.closed) {
            w.document.open();
            w.document.write('<!doctype html><html lang="ru"><meta charset="utf-8"><title>Ошибка формирования отчёта</title><body><h1>Не удалось сформировать отчёт</h1><p id="report-error"></p></body></html>');
            w.document.close();
            const detail = e instanceof Error ? e.message : String(e);
            w.document.getElementById('report-error').textContent = detail || 'Обновите страницу профиля и повторите попытку.';
        }
        console.error('Report generation failed', e);
    }
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
function resetHistoryPage() { historyOffset = 0; loadHistory(); }
function changeHistoryPage(direction) { historyOffset = Math.max(0, historyOffset + direction * 50); loadHistory(); }
function copyResultCoords(id) {
    const r = window._histTasks?.[id]?.result;
    if (r) copyClip(`${r.latitude.toFixed(8)}\t${r.longitude.toFixed(8)}\t${r.height.toFixed(4)}`);
}
function compareResult(id) {
    const task = window._histTasks?.[id];
    if (!task?.result) return;
    if (comparisonTasks.has(id)) comparisonTasks.delete(id);
    else {
        if (comparisonTasks.size === 2) comparisonTasks.clear();
        // This comparison exists only in page memory.
        comparisonTasks.set(id, task);
    }
    const panel = document.getElementById('comparisonPanel');
    panel.hidden = comparisonTasks.size === 0;
    const tasks = [...comparisonTasks.values()];
    panel.innerHTML = '<h3>Сравнение результатов</h3><p>' + tasks.map(t => escHtml(t.filename)).join(' ↔ ') + '</p>';
    if (tasks.length === 1) panel.innerHTML += '<p>Нажмите «Сравнить» у второго результата.</p>';
    if (tasks.length === 2) {
        const [a,b] = tasks.map(t => t.result);
        panel.innerHTML += `<p>Второй минус первый: ΔB ${(b.latitude-a.latitude).toFixed(8)}°, ΔL ${(b.longitude-a.longitude).toFixed(8)}°, ΔH ${(b.height-a.height).toFixed(4)} м.</p>
          <p>FIX: ${Number(a.fixRate).toFixed(1)}% → ${Number(b.fixRate).toFixed(1)}%.</p>
          <p class="workflow-note">Сопоставляйте одну точку, одну систему координат и эпоху. Разность решений сама по себе не является оценкой точности. Для кинематики здесь сравниваются сводные координаты, а не все эпохи траекторий.</p>`;
    }
    panel.innerHTML += '<button class="workflow-button" onclick="comparisonTasks.clear(); document.getElementById(\'comparisonPanel\').hidden=true">Очистить сравнение</button>';
    panel.scrollIntoView({block:'center', behavior:'smooth'});
}
function repeatSettings(id) {
    const config = window._histTasks?.[id]?.config;
    if (!config) return;
    const child = window.open('/measurements', '_blank');
    if (!child) { showToast('Разрешите открытие вкладки для повторного запуска', 'err'); return; }
    const listener = event => {
        if (event.origin !== location.origin || event.source !== child || event.data?.type !== 'processing-ready') return;
        child.postMessage({type:'processing-settings', config}, location.origin);
        window.removeEventListener('message', listener);
    };
    window.addEventListener('message', listener);
    setTimeout(() => window.removeEventListener('message', listener), 30000);
}

// Do not retain comparison data after its existing result expiry.
setInterval(() => {
    if ([...comparisonTasks.values()].some(t => new Date(t.resultExpiresAt).getTime() <= Date.now())) {
        comparisonTasks.clear();
        const panel = document.getElementById('comparisonPanel');
        panel.textContent = ''; panel.hidden = true;
    }
}, 30000);
