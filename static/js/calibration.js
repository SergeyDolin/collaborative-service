/* calibration.js — мастер определения фазового центра антенны смартфона */

'use strict';

// ─── Состояние ──────────────────────────────────────────────────────────────
const state = {
    step: 1,
    mode: '',
    refType: '',
    deviceId: 0,
    deviceModel: '',
    taskId: '',
    receiverPppTaskId: '',
    receiverFile: null,
    sessions: [],
    pollTimer: null,
};

const REQUIRED_SESSIONS = {
    full: [
        { position: 'vertical',   orientation: 'north' },
        { position: 'vertical',   orientation: 'east'  },
        { position: 'vertical',   orientation: 'south' },
        { position: 'vertical',   orientation: 'west'  },
        { position: 'horizontal', orientation: 'north' },
        { position: 'horizontal', orientation: 'east'  },
        { position: 'horizontal', orientation: 'south' },
        { position: 'horizontal', orientation: 'west'  },
    ],
    horizontal_only: [
        { position: 'vertical', orientation: 'north' },
        { position: 'vertical', orientation: 'east'  },
        { position: 'vertical', orientation: 'south' },
        { position: 'vertical', orientation: 'west'  },
    ],
    quick: [
        { position: 'vertical', orientation: 'north' },
    ],
};

const MODE_INFO = {
    full:             'Полная калибровка: 4 вертикальных (N/E/S/W) + 4 горизонтальных (N/E/S/W). Определяются все три компоненты фазового центра. Требует опорной точки.',
    horizontal_only:  'Горизонтальная калибровка: 4 вертикальных сеанса по сторонам света. Определяются только горизонтальные компоненты — опорная точка необязательна.',
    quick:            'Быстрая калибровка: 1 вертикальный сеанс строго на север + опорная точка. Результат действителен 12 часов. Необходима опора (марка или приёмник).',
};

const POS_LABEL    = { vertical: 'Вертикальное', horizontal: 'Горизонтальное' };
const ORIENT_LABEL = { north: 'Север ↑', east: 'Восток →', south: 'Юг ↓', west: 'Запад ←' };
const ORIENT_ICON  = { north: '⬆', east: '➡', south: '⬇', west: '⬅' };

// ─── Утилиты ─────────────────────────────────────────────────────────────────
function token() {
    const t = localStorage.getItem('token');
    if (!t) { window.location.href = '/login'; return null; }
    return t;
}

async function apiFetch(url, opts = {}) {
    const t = token();
    if (!t) return null;
    opts.headers = { ...(opts.headers || {}), 'Authorization': 'Bearer ' + t };
    const res = await fetch(url, opts);
    if (res.status === 401) { window.location.href = '/login'; return null; }
    return res;
}

function fmt(v, decimals = 1) {
    if (v === null || v === undefined || isNaN(v)) return '—';
    return (v * 1000).toFixed(decimals);
}

// ─── Навигация ───────────────────────────────────────────────────────────────
function goStep(n) {
    for (let i = 1; i <= 6; i++) document.getElementById('step' + i).classList.add('hidden');
    document.getElementById('step' + n).classList.remove('hidden');

    document.querySelectorAll('.wizard-step').forEach(el => {
        const s = +el.dataset.step;
        el.classList.remove('active', 'done');
        if (s === n) el.classList.add('active');
        else if (s < n) el.classList.add('done');
    });

    state.step = n;
    if (n === 5) renderSessionList();
}

// ─── Шаг 1 ───────────────────────────────────────────────────────────────────
async function loadDevices() {
    const sel = document.getElementById('deviceSelect');
    const res = await apiFetch('/api/devices');
    if (!res || !res.ok) return;
    const devices = await res.json();
    sel.innerHTML = '<option value="">— не выбирать —</option>';
    (devices || []).forEach(d => {
        const opt = document.createElement('option');
        opt.value = d.id;
        opt.textContent = d.name || d.model || ('Устройство #' + d.id);
        sel.appendChild(opt);
    });
}

// ─── Шаг 2 ───────────────────────────────────────────────────────────────────
function selectMode(mode) {
    state.mode = mode;
    document.querySelectorAll('.mode-card').forEach(c => c.classList.remove('selected'));
    event.currentTarget.classList.add('selected');

    const detail = document.getElementById('modeDetail');
    detail.textContent = MODE_INFO[mode];
    detail.classList.remove('hidden');
    document.getElementById('step2Next').disabled = false;
}

// ─── Шаг 3 ───────────────────────────────────────────────────────────────────
function selectRef(type) {
    if (state.mode === 'quick' && type === 'none') {
        showToast('Быстрый режим требует опорной точки', 'err');
        return;
    }
    state.refType = type;
    ['geodetic', 'receiver', 'none'].forEach(t => {
        document.getElementById('ref' + t.charAt(0).toUpperCase() + t.slice(1))
            .classList.toggle('selected', t === type);
    });

    document.getElementById('refGeodeticForm').classList.toggle('hidden', type !== 'geodetic');
    document.getElementById('refReceiverForm').classList.toggle('hidden', type !== 'receiver');
    document.getElementById('step3Next').disabled = false;
}

function handleReceiverFile(input) {
    const f = input.files[0];
    if (!f) return;
    state.receiverFile = f;
    document.getElementById('receiverDropText').textContent = f.name;
}

// ─── Шаг 4 ───────────────────────────────────────────────────────────────────
async function startCalibTask() {
    const t = token(); if (!t) return;

    state.deviceModel = document.getElementById('deviceModel').value.trim();

    const body = {
        deviceId:    state.deviceId,
        deviceModel: state.deviceModel,
        mode:        state.mode,
        refType:     state.refType,
        refLat:      parseFloat(document.getElementById('refLat')?.value)  || 0,
        refLon:      parseFloat(document.getElementById('refLon')?.value)  || 0,
        refH:        parseFloat(document.getElementById('refH')?.value)    || 0,
        reduceH:     parseFloat(document.getElementById('reduceH').value)  || 0,
        reduceE:     parseFloat(document.getElementById('reduceE').value)  || 0,
        reduceN:     parseFloat(document.getElementById('reduceN').value)  || 0,
    };

    const res = await apiFetch('/api/calibration/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
    });
    if (!res || !res.ok) { showToast('Ошибка создания задачи', 'err'); return; }
    const data = await res.json();
    state.taskId = data.taskId;

    if (state.refType === 'receiver' && state.receiverFile) {
        const fd = new FormData();
        fd.append('file', state.receiverFile);
        const r2 = await apiFetch('/api/calibration/' + state.taskId + '/receiver', { method: 'POST', body: fd });
        if (r2 && r2.ok) {
            state.receiverPppTaskId = (await r2.json()).receiverPppTaskId;
        }
    }

    const req = REQUIRED_SESSIONS[state.mode] || [];
    const noun = req.length === 1 ? 'сеанс' : req.length < 5 ? 'сеанса' : 'сеансов';
    document.getElementById('sessionsRequired').innerHTML =
        `Для режима <strong>${{ full: 'полная', horizontal_only: 'горизонт.', quick: 'быстрая' }[state.mode]}</strong> `
        + `нужно ${req.length} ${noun}: `
        + req.map(s => `${POS_LABEL[s.position]} · ${ORIENT_LABEL[s.orientation]}`).join(', ') + '.';

    goStep(5);
}

// ─── Шаг 5 ───────────────────────────────────────────────────────────────────
async function uploadSession() {
    const t = token(); if (!t) return;
    if (!state.taskId) { showToast('Сначала завершите шаг 4', 'err'); return; }

    const fileInput = document.getElementById('sessFile');
    const file = fileInput.files[0];
    if (!file) { showToast('Выберите файл', 'err'); return; }

    const position    = document.getElementById('sessPosition').value;
    const orientation = document.getElementById('sessOrientation').value;

    const btn = document.getElementById('addSessBtn');
    btn.disabled = true;
    btn.textContent = 'Загрузка…';

    const fd = new FormData();
    fd.append('file', file);
    fd.append('position', position);
    fd.append('orientation', orientation);

    const res = await apiFetch('/api/calibration/' + state.taskId + '/session', {
        method: 'POST', body: fd,
    });
    btn.textContent = 'Загрузить сеанс';

    if (!res || !res.ok) {
        const err = res ? await res.json().catch(() => ({})) : {};
        showToast(err.message || 'Ошибка загрузки сеанса', 'err');
        btn.disabled = false;
        return;
    }

    const data = await res.json();
    state.sessions.push({
        id: data.sessionId,
        position, orientation,
        pppTaskId: data.pppTaskId,
        status: 'pending',
        filename: file.name,
    });

    fileInput.value = '';
    document.getElementById('sessDropText').textContent = 'Перетащите RINEX-файл сеанса или нажмите для выбора';
    btn.disabled = true;

    renderSessionList();
    updateStep5Next();
}

function renderSessionList() {
    const list = document.getElementById('sessionList');
    if (state.sessions.length === 0) {
        list.innerHTML = '<div style="color:var(--ink-faint);font-size:0.82rem;padding:8px 0">Сеансы ещё не добавлены</div>';
        return;
    }
    list.innerHTML = state.sessions.map(s => `
        <div class="session-item">
          <div class="session-icon">${ORIENT_ICON[s.orientation] || '📍'}</div>
          <div class="session-meta">
            <div class="session-name">${s.filename}</div>
            <div class="session-sub">${POS_LABEL[s.position]} · ${ORIENT_LABEL[s.orientation]}</div>
          </div>
          <span class="session-status ${s.status === 'done' ? 'done' : 'pending'}">${s.status === 'done' ? 'готово' : 'ожидание'}</span>
        </div>
    `).join('');
}

function updateStep5Next() {
    const req = REQUIRED_SESSIONS[state.mode] || [];
    document.getElementById('step5Next').disabled = state.sessions.length < Math.max(1, req.length);
}

async function submitCalibration() {
    const t = token(); if (!t) return;
    const res = await apiFetch('/api/calibration/' + state.taskId + '/submit', { method: 'POST' });
    if (!res || !res.ok) { showToast('Ошибка запуска вычисления', 'err'); return; }
    goStep(6);
    startPolling();
}

// ─── Шаг 6 ───────────────────────────────────────────────────────────────────
function startPolling() {
    if (state.pollTimer) clearInterval(state.pollTimer);
    state.pollTimer = setInterval(pollStatus, 8000);
    pollStatus();
}

async function pollStatus() {
    if (!state.taskId) return;
    const res = await apiFetch('/api/calibration/' + state.taskId + '/status');
    if (!res || !res.ok) return;
    const task = await res.json();

    const sessions = task.sessions || [];
    const done = sessions.filter(s => s.status === 'completed' || s.status === 'failed').length;
    const pct = Math.round(done / Math.max(sessions.length, 1) * 80);
    document.getElementById('pollBar').style.width = pct + '%';

    document.getElementById('sessStatusList').innerHTML = sessions.map(s =>
        `<div style="padding:2px 0">${POS_LABEL[s.position]} · ${ORIENT_LABEL[s.orientation]} — `
        + `<span style="color:${s.status === 'completed' ? 'var(--ok)' : s.status === 'failed' ? 'var(--err)' : 'var(--ink-3)'}">`
        + `${s.status === 'completed' ? 'готово' : s.status === 'failed' ? 'ошибка' : 'обработка…'}</span></div>`
    ).join('');

    if (task.status === 'completed' && task.result) {
        clearInterval(state.pollTimer);
        document.getElementById('pollBar').style.width = '100%';
        showResult(task.result);
    } else if (task.status === 'failed') {
        clearInterval(state.pollTimer);
        document.getElementById('resultPolling').classList.add('hidden');
        document.getElementById('resultError').textContent = 'Ошибка: ' + (task.errorMessage || 'неизвестная ошибка');
        document.getElementById('resultError').classList.remove('hidden');
    } else {
        document.getElementById('pollStatus').textContent =
            ({ pending: 'Ожидание…', processing: 'Обработка сеансов PPP…' })[task.status] || 'Обработка…';
    }
}

function showResult(r) {
    document.getElementById('resultPolling').classList.add('hidden');
    document.getElementById('resultContent').classList.remove('hidden');

    document.getElementById('resLeft').textContent  = fmt(r.offsetLeft);
    document.getElementById('resDepth').textContent = fmt(r.offsetDepth);
    document.getElementById('resDown').textContent  = isNaN(r.offsetDown) ? '—' : fmt(r.offsetDown);

    document.getElementById('sigLeft').textContent  = r.sigmaLeft  ? `σ = ${fmt(r.sigmaLeft)} мм`  : '';
    document.getElementById('sigDepth').textContent = r.sigmaDepth ? `σ = ${fmt(r.sigmaDepth)} мм` : '';
    document.getElementById('sigDown').textContent  = r.sigmaDown && !isNaN(r.sigmaDown) ? `σ = ${fmt(r.sigmaDown)} мм` : '';

    if (r.validUntil) {
        const until = new Date(r.validUntil);
        const vbox = document.getElementById('validUntilBox');
        vbox.textContent = `Быстрая калибровка действительна до ${until.toLocaleString('ru-RU')}. `
            + 'Смартфон должен быть строго ориентирован на север при работе.';
        vbox.classList.remove('hidden');
    }

    if (r.sessions && r.sessions.length > 0) {
        const rows = r.sessions.map(s => `
            <tr>
              <td>${POS_LABEL[s.position]}</td>
              <td>${ORIENT_LABEL[s.orientation]}</td>
              <td style="font-family:var(--font-mono);text-align:right">${(s.deltaE * 1000).toFixed(1)}</td>
              <td style="font-family:var(--font-mono);text-align:right">${(s.deltaN * 1000).toFixed(1)}</td>
              <td style="font-family:var(--font-mono);text-align:right">${(s.deltaU * 1000).toFixed(1)}</td>
              <td style="font-family:var(--font-mono);text-align:right">${s.fixRate ? s.fixRate.toFixed(1) + '%' : '—'}</td>
            </tr>`).join('');
        document.getElementById('sessionTable').innerHTML = `
            <div class="section-label" style="margin-top:24px">По сеансам</div>
            <table style="width:100%;border-collapse:collapse;font-size:0.8rem">
              <thead><tr style="border-bottom:1px solid var(--paper-edge)">
                <th style="text-align:left;padding:6px 4px;color:var(--ink-3)">Положение</th>
                <th style="text-align:left;padding:6px 4px;color:var(--ink-3)">Ориент.</th>
                <th style="text-align:right;padding:6px 4px;color:var(--ink-3)">ΔE (мм)</th>
                <th style="text-align:right;padding:6px 4px;color:var(--ink-3)">ΔN (мм)</th>
                <th style="text-align:right;padding:6px 4px;color:var(--ink-3)">ΔU (мм)</th>
                <th style="text-align:right;padding:6px 4px;color:var(--ink-3)">Fix</th>
              </tr></thead>
              <tbody>${rows}</tbody>
            </table>`;
    }
}

// ─── Drop zone ────────────────────────────────────────────────────────────────
function setupDropZone(zoneId, inputId, onFile) {
    const zone = document.getElementById(zoneId);
    if (!zone) return;
    zone.addEventListener('dragover', e => { e.preventDefault(); zone.classList.add('dragover'); });
    zone.addEventListener('dragleave', () => zone.classList.remove('dragover'));
    zone.addEventListener('drop', e => {
        e.preventDefault();
        zone.classList.remove('dragover');
        const f = e.dataTransfer.files[0];
        if (f) {
            const inp = document.getElementById(inputId);
            const dt = new DataTransfer();
            dt.items.add(f);
            inp.files = dt.files;
            onFile(f);
        }
    });
}

// ─── Toast ────────────────────────────────────────────────────────────────────
function showToast(msg, type = 'info') {
    const t = document.createElement('div');
    t.style.cssText = `position:fixed;bottom:24px;right:24px;z-index:9999;
        background:${type === 'err' ? 'var(--err-bg)' : 'var(--paper-2)'};
        color:${type === 'err' ? 'var(--err)' : 'var(--ink)'};
        border:1px solid ${type === 'err' ? 'var(--err)' : 'var(--paper-edge)'};
        border-radius:var(--r-sm);padding:12px 18px;font-size:0.85rem;
        box-shadow:0 4px 16px rgba(0,0,0,0.12);max-width:320px`;
    t.textContent = msg;
    document.body.appendChild(t);
    setTimeout(() => t.remove(), 4000);
}

// ─── Init ─────────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
    const t = token(); if (!t) return;

    // Привязать обработчики после появления DOM
    document.getElementById('deviceSelect').addEventListener('change', function () {
        if (this.value) state.deviceId = +this.value;
    });
    document.getElementById('deviceModel').addEventListener('input', function () {
        state.deviceModel = this.value.trim();
    });
    document.getElementById('sessFile').addEventListener('change', function () {
        if (this.files[0]) {
            document.getElementById('sessDropText').textContent = this.files[0].name;
            document.getElementById('addSessBtn').disabled = false;
        }
    });
    document.getElementById('sessOrientation').addEventListener('change', () => {
        document.getElementById('compassWarn').style.display = 'block';
    });

    setupDropZone('receiverDrop', 'receiverFile', f => {
        state.receiverFile = f;
        document.getElementById('receiverDropText').textContent = f.name;
    });
    setupDropZone('sessDrop', 'sessFile', f => {
        document.getElementById('sessDropText').textContent = f.name;
        document.getElementById('addSessBtn').disabled = false;
    });

    loadDevices();
    goStep(1);
});
