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
    document.getElementById('heroName').textContent  = p.fullName || login;
    document.getElementById('headerLogin').textContent = login;

    if (p.createdAt) {
        const d = new Date(p.createdAt).toLocaleDateString('ru', { year:'numeric', month:'long' });
        document.getElementById('heroMeta').textContent = 'Участник с ' + d;
    }

    if (p.avatar) {
        document.getElementById('heroAvatarImg').src = p.avatar;
        document.getElementById('heroAvatarImg').style.display = 'block';
        document.getElementById('heroAvatarFallback').style.display = 'none';
        document.getElementById('editAvatarPreview').src = p.avatar;
        document.getElementById('editAvatarPreview').style.display = 'block';
        document.getElementById('editAvatarFallback').style.display = 'none';
    }
    document.getElementById('editFullName').value = p.fullName || '';
}

/* ════════════════════════════════════════════
   DEVICES
════════════════════════════════════════════ */
const DEVICE_ICONS  = { gnss_receiver:'📡', smartphone:'📱', tablet:'📟', other:'🔧' };
const DEVICE_LABELS = { gnss_receiver:'ГНСС-приёмник', smartphone:'Смартфон', tablet:'Планшет', other:'Иное' };
const MOUNT_LABELS  = { car:'Автомобиль', permanent_station:'Пост. станция', uav:'БПЛА', rod:'Веха',  man: "Человек" };

function renderDeviceExtra(d) {
    if (d.deviceType === 'gnss_receiver') {
        if (!d.antennaName) return '';
        const enu = (d.antennaE || d.antennaN || d.antennaU)
            ? `<div class="dev-antenna-enu">ENU: ${(+d.antennaE).toFixed(3)} / ${(+d.antennaN).toFixed(3)} / ${(+d.antennaU).toFixed(3)} м</div>`
            : '';
        return `<div class="dev-antenna">${escHtml(d.antennaName)}</div>${enu}`;
    }
    if (d.phaseCenterMethod === 'auto' && d.phaseCenterValidUntil) {
        const until = new Date(d.phaseCenterValidUntil);
        const now   = new Date();
        const expired = until < now;
        const fmt = until.toLocaleString('ru', { day:'2-digit', month:'2-digit', hour:'2-digit', minute:'2-digit' });
        return `<div class="dev-pc ${expired ? 'dev-pc-expired' : 'dev-pc-ok'}">
            ${expired ? '⚠️ Калибровка истекла' : '🤖 Авто до ' + fmt}
        </div>`;
    }
    if (d.phaseCenterMethod === 'manual' && (d.antennaE || d.antennaN || d.antennaU)) {
        return `<div class="dev-antenna-enu">ENU: ${(+d.antennaE).toFixed(3)} / ${(+d.antennaN).toFixed(3)} / ${(+d.antennaU).toFixed(3)} м</div>`;
    }
    return '';
}

function renderDevices(devices) {
    const el = document.getElementById('devicesContent');
    if (!devices || devices.length === 0) {
        el.innerHTML = `<div class="no-devices"><span class="nd-icon">📡</span>Нет зарегистрированных устройств.<br>Добавьте устройство для участия в коллаборативном позиционировании.</div>`;
        return;
    }
    el.innerHTML = `<div class="devices-grid">${devices.map(d => `
        <div class="device-card">
            <button class="dev-delete" onclick="deleteDevice(${d.id})" title="Удалить">✕</button>
            <span class="dev-icon">${DEVICE_ICONS[d.deviceType] || '🔧'}</span>
            <div class="dev-name">${escHtml(d.name)}</div>
            <div class="dev-badges">
                <span class="badge badge-type">${DEVICE_LABELS[d.deviceType] || d.deviceType}</span>
                <span class="badge badge-mount">${MOUNT_LABELS[d.mountType] || d.mountType}</span>
            </div>
            ${renderDeviceExtra(d)}
            ${d.description ? `<div class="dev-desc">${escHtml(d.description)}</div>` : ''}
        </div>`).join('')}
        <button class="add-device-card" onclick="openAddDevice()">
            <span class="add-icon">＋</span><span>Добавить устройство</span>
        </button>
    </div>`;
}

function openAddDevice() {
    newDevType = null; newMountType = null; newPcMethod = null;
    document.querySelectorAll('.tc').forEach(c => c.classList.remove('chosen'));
    document.querySelectorAll('.mc').forEach(c => c.classList.remove('chosen'));
    document.querySelectorAll('.pc-card').forEach(c => c.classList.remove('chosen'));
    document.getElementById('newDevName').value   = '';
    document.getElementById('newDevDesc').value   = '';
    document.getElementById('devAntennaName').value = '';
    document.getElementById('devAntennaE').value  = '0';
    document.getElementById('devAntennaN').value  = '0';
    document.getElementById('devAntennaU').value  = '0';
    document.getElementById('devPcE').value = '0';
    document.getElementById('devPcN').value = '0';
    document.getElementById('devPcU').value = '0';
    document.getElementById('devAntennaField').style.display       = 'none';
    document.getElementById('devAntennaOffsetField').style.display = 'none';
    document.getElementById('devPhaseCenterField').style.display   = 'none';
    document.getElementById('devAutoWarning').style.display   = 'none';
    document.getElementById('devManualOffsets').style.display = 'none';
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

    newPcMethod = null;
    document.querySelectorAll('.pc-card').forEach(c => c.classList.remove('chosen'));
    document.getElementById('devAutoWarning').style.display   = 'none';
    document.getElementById('devManualOffsets').style.display = 'none';
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
        const r = await fetch('/api/devices', {
            method: 'POST',
            headers: { 'Authorization': `Bearer ${getToken()}`, 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        if (!r.ok) {
            const err = await r.json().catch(() => ({}));
            showToast(err.error || 'Ошибка добавления', 'err');
            return;
        }
        closeModal('addDeviceModal');
        showToast('Устройство добавлено');
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
   EDIT PROFILE
════════════════════════════════════════════ */
function openEditProfile() { openModal('editProfileModal'); }
function previewEditAvatar(input) {
    const f = input.files[0]; if (!f) return;
    const r = new FileReader();
    r.onload = e => {
        document.getElementById('editAvatarPreview').src = e.target.result;
        document.getElementById('editAvatarPreview').style.display = 'block';
        document.getElementById('editAvatarFallback').style.display = 'none';
    };
    r.readAsDataURL(f);
}
async function saveProfile() {
    const fd = new FormData();
    fd.append('fullName', document.getElementById('editFullName').value.trim());
    const avatarFile = document.getElementById('editAvatarFile').files[0];
    if (avatarFile) fd.append('avatar', avatarFile);
    try {
        const r = await fetch('/api/profile/update', { method: 'POST', headers: { 'Authorization': `Bearer ${getToken()}` }, body: fd });
        if (!r.ok) throw new Error();
        closeModal('editProfileModal');
        showToast('Профиль сохранён');
        loadProfile();
    } catch { showToast('Ошибка сохранения', 'err'); }
}

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
    return { absolute:'📍 SPP', single:'📍 SPP', relative:'🔗 Относительный', ppp:'🎯 PPP-AR' }[m?.toLowerCase()] || m || '—';
}
function getStatusText(s) {
    return { pending:'⏳ В очереди', processing:'🔄 Обработка…', completed:'✅ Завершено', failed:'❌ Ошибка' }[s] || s;
}
function getSolutionStatus(q) {
    if (q===1) return '<span class="fix-badge">🎯 FIX</span>';
    if (q===6) return '<span class="float-badge">📊 FLOAT</span>';
    if (q===0) return '<span style="color:var(--err)">❌ NO FIX</span>';
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
        const tasks = await r.json();
        if (!tasks || tasks.length === 0) {
            el.innerHTML = '<div class="empty-history">📭 История обработок пуста</div>';
            document.getElementById('btnDeleteAll').style.display = 'none';
            document.getElementById('statsGrid').style.display = 'none';
            return;
        }
        document.getElementById('btnDeleteAll').style.display = selectMode ? 'none' : 'block';
        updateStats(tasks);
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
                if (r.lastSolutionLine) {
                    const f = r.lastSolutionLine.trim().split(/\s+/);
                    const lat = f[2] ? parseFloat(f[2]) : null;
                    const lon = f[3] ? parseFloat(f[3]) : null;
                    const h   = f[4] ? parseFloat(f[4]) : null;
                    const q   = f[5] ? parseInt(f[5]) : null;
                    coordsHtml = `<div class="coords-mono">${lat!==null?`B: ${lat.toFixed(8)}°<br>`:''}${lon!==null?`L: ${lon.toFixed(8)}°<br>`:''}${h!==null?`H: ${h.toFixed(4)} м`:''}</div>`;
                } else if (hasCoords) {
                    coordsHtml = `<div class="coords-mono">B: ${formatCoord(r.latitude)}°<br>L: ${formatCoord(r.longitude)}°<br>H: ${formatH(r.height)} м</div>`;
                }
                const dlBtn = task.fileType !== 'static'
                    ? `<button class="download-btn" onclick="downloadResult('${task.id}',event)">📥 Скачать .pos</button>` : '';
                const trBtn = hasCoords
                    ? `<button class="btn-transform" onclick="openTransform(${r.latitude},${r.longitude},${r.height||0},'${task.id}')">🔄 Пересчёт</button>` : '';
                resultHtml = `<div class="result-block">
                    <div class="stats-info">${getSolutionStatus(r.q)}${fixRate?` <span>(${fixRate}%)</span>`:''} ${r.nSat?`<span>🛰️ ${r.nSat}</span>`:''}</div>
                    ${coordsHtml}
                    <div class="action-buttons">${dlBtn}${trBtn}</div>
                </div>`;
            }
            const errHtml = (status==='failed'&&task.errorMessage) ? `<div class="error-msg">❌ ${escHtml(task.errorMessage)}</div>` : '';
            return `<div class="history-item" data-task-id="${task.id}">
                <input type="checkbox" class="task-checkbox" data-id="${task.id}" onchange="onCbChange(this)">
                <div class="item-body">
                    <div class="history-header-row">
                        <div class="history-info">
                            <div class="history-date">📅 ${date}</div>
                            <div class="history-method">${method} · ${mode}</div>
                            <div class="history-file">📁 ${escHtml(task.filename||'—')}</div>
                        </div>
                        <div class="history-status">
                            <span class="status-badge status-${status}">${getStatusText(status)}</span>
                            <button class="btn-delete-single" onclick="confirmDeleteOne('${task.id}')">🗑️ Удалить</button>
                        </div>
                    </div>
                    ${resultHtml}${errHtml}
                </div>
            </div>`;
        }).join('');
        if (selectMode) el.classList.add('select-mode');
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
    btn.textContent = selectMode ? '✖ Отмена' : '☑️ Выбрать';
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
            document.getElementById('obsDateInfo').innerHTML = `<span style="color:var(--ok);">✓ Дата наблюдения: ${new Date(d.date).toLocaleDateString('ru')}</span>`;
        } else { document.getElementById('obsDateInfo').textContent = '⚠️ Не удалось определить дату'; document.getElementById('sourceEpoch').value = new Date().toISOString().slice(0,10); }
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
            const meta   = `<div style="font-size:0.7rem;color:var(--muted);margin-bottom:10px;padding:8px;background:#dbeafe;border-radius:6px;line-height:1.6;"><div>📐 <strong>${srcCRS}</strong> ${srcL} → <strong>${tgtCRS}</strong> ${tgtL}</div><div style="font-family:'JetBrains Mono',monospace;font-size:0.62rem;color:#93c5fd;word-break:break-all;">${opCode}</div></div>`;
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