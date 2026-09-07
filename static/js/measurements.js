let preflightBusy = false, preflightBlocked = false, inspectedHeader = null;
let savedDevices = [], selectedProfileDevice = null;
let selectedMethod = null;
let selectedFile = null;
let selectedDeviceType = null;

const methodDetails = {
    single: {
        title: "Абсолютный метод (SPP)",
        description: "Абсолютный геометрический метод. Позиционирование с использованием псевдодальностей и бортовых эфемерид.",
        features: [
            "Не требует базовой станции",
            "Быстрая обработка",
            "Подходит для навигации"
        ],
        config: { 
            method: "single", 
            mode: "kinematic",
            frequency: "l1+l2", 
            elevationMask: 15, 
            ionoModel: "brdc", 
            tropModel: "saas", 
            arMode: "off", 
            tideCorr: false, 
            satelliteSystem: 61 
        }
    },
    "ppp-kinematic": {
        title: "PPP-AR Кинематика",
        description: "Метод Precise Point Positioning с разрешением неоднозначностей для движущихся объектов.",
        features: [
            "Не требует базовой станции",
            "Разрешение фазовых неоднозначностей (AR)",
            "Для движущихся объектов"
        ],
        config: { 
            method: "ppp", 
            mode: "kinematic", 
            frequency: "l1+l2+l5", 
            elevationMask: 15, 
            ionoModel: "dual-freq", 
            tropModel: "est-ztd", 
            arMode: "continuous", 
            tideCorr: true, 
            usePreciseEphemeris: true, 
            usePreciseClock: true, 
            useDcb: true, 
            useErp: true, 
            useOsb: true,
            satelliteSystem: 61 
        }
    },
    "ppp-static": {
        title: "PPP-AR Статика",
        description: "Метод Precise Point Positioning с разрешением неоднозначностей для неподвижных объектов.",
        features: [
            "Не требует базовой станции",
            "Разрешение фазовых неоднозначностей (AR)",
            "Для неподвижных объектов"
        ],
        config: { 
            method: "ppp", 
            mode: "static", 
            frequency: "l1+l2+l5", 
            elevationMask: 15, 
            ionoModel: "dual-freq", 
            tropModel: "est-ztd", 
            arMode: "continuous", 
            tideCorr: true, 
            usePreciseEphemeris: true, 
            usePreciseClock: true, 
            useDcb: true, 
            useErp: true, 
            useOsb: true,
            satelliteSystem: 61 
        }
    }
};

function checkAuth() {
    const token = localStorage.getItem('token');
    const login = localStorage.getItem('userLogin');
    const userMenu = document.getElementById('userMenu');
    
    if (token && login) {
        userMenu.innerHTML = `
            <span class="user-name">${login}</span>
            <button class="logout-btn" onclick="logout()">Выйти</button>
        `;
    } else {
        window.location.href = '/login';
    }
}

function logout() {
    localStorage.removeItem('token');
    localStorage.removeItem('userLogin');
    window.location.href = '/';
}

function selectMethod(method) {
    selectedMethod = method;
    
    document.querySelectorAll('.method-option[data-method]').forEach(opt => {
        opt.classList.remove('selected');
    });
    
    document.querySelector(`[data-method="${method}"]`).classList.add('selected');
    
    const detail = methodDetails[method];
    document.getElementById('methodDetail').innerHTML = `
        <h4>${detail.title}</h4>
        <p>${detail.description}</p>
        <ul class="method-features">${detail.features.map(f => `<li><span class="mf-check" data-icon="check" data-icon-size="14"></span>${f}</li>`).join('')}</ul>
    `;
    if (window.applyIcons) window.applyIcons(document.getElementById('methodDetail'));
    
    document.getElementById('deviceSection').style.display = 'block';
    updateButtonState();
}

function selectDeviceType(type) {
    if (selectedProfileDevice && (selectedProfileDevice.deviceType === 'gnss_receiver' ? 'gnss' : 'mobile') !== type) {
        selectedProfileDevice = null;
        document.getElementById('savedDevice').value = '';
        document.getElementById('deviceSource').textContent = 'Источник: ручные параметры или заголовок RINEX.';
        document.getElementById('deviceValidity').textContent = '';
    }
    selectedDeviceType = type;
    document.querySelectorAll('.device-option').forEach(el => el.classList.remove('selected'));
    document.querySelector(`.device-option[data-device="${type}"]`).classList.add('selected');
    document.getElementById('mobileAntennaSection').style.display = type === 'mobile' ? 'block' : 'none';
    if (type === 'mobile') checkMobileWarning();
    updateButtonState();
}

function checkMobileWarning() {
    const e = parseFloat(document.getElementById('mobileE').value) || 0;
    const n = parseFloat(document.getElementById('mobileN').value) || 0;
    const u = parseFloat(document.getElementById('mobileU').value) || 0;
    document.getElementById('mobileAccuracyWarning').style.display = (e === 0 && n === 0 && u === 0) ? 'block' : 'none';
}

function setBtnText(text) {
    const btn = document.getElementById('processBtn');
    if (!btn) return;
    const el = document.getElementById('processBtnText');
    if (el) el.textContent = text;
    else btn.textContent = text;
}

function setBtnProgress(pct) {
    const btn = document.getElementById('processBtn');
    if (!btn) return;
    btn.style.setProperty('--up', pct + '%');
}

function updateButtonState() {
    const btn = document.getElementById('processBtn');
    const advice = Workflow.preflightAdvice(inspectedHeader, selectedMethod);
    document.getElementById('preflightAdvice').textContent = advice;
    document.getElementById('preflightAdvice').hidden = !advice;
    if (preflightBusy || preflightBlocked) {
        btn.disabled = true;
        setBtnText(preflightBusy ? 'Проверка файла…' : 'Выберите корректный файл наблюдений');
        return;
    }
    const ready = selectedMethod && selectedFile && selectedDeviceType;
    if (ready) {
        btn.disabled = false;
        setBtnText('Запустить обработку');
    } else if (!selectedMethod) {
        btn.disabled = true;
        setBtnText('Выберите метод и файл');
    } else if (!selectedDeviceType) {
        btn.disabled = true;
        setBtnText('Выберите тип устройства');
    } else if (!selectedFile) {
        btn.disabled = true;
        setBtnText('Выберите файл наблюдений');
    } else {
        btn.disabled = true;
        setBtnText('Выберите метод и файл');
    }
}

// Drag & Drop
const fileZone = document.getElementById('fileZone');
fileZone.addEventListener('dragover', (e) => { 
    e.preventDefault(); 
    fileZone.classList.add('drag-over'); 
});

fileZone.addEventListener('dragleave', () => { 
    fileZone.classList.remove('drag-over'); 
});

fileZone.addEventListener('drop', (e) => {
    e.preventDefault();
    fileZone.classList.remove('drag-over');
    if (e.dataTransfer.files.length > 0) {
        handleFile(e.dataTransfer.files[0]);
    }
});

document.getElementById('fileInput').addEventListener('change', (e) => { 
    if (e.target.files.length > 0) {
        handleFile(e.target.files[0]);
    }
});

function handleFile(file) {
    const maxSize = 1 * 1024 * 1024 * 1024; // 1 GB
    
    if (file.size === 0 || file.size > maxSize) {
        selectedFile = null;
        preflightBusy = false; preflightBlocked = false;
        document.getElementById('filePreview').hidden = true;
        document.getElementById('fileInfo').textContent = 'Файл не выбран';
        updateButtonState();
        const statusDiv = document.getElementById('status');
        statusDiv.className = 'status-message status-error';
        statusDiv.style.display = 'block';
        statusDiv.textContent = file.size === 0 ? "Файл пуст. Выберите файл с наблюдениями." : `❌ Файл слишком большой. Максимальный размер: 1 GB. Ваш файл: ${(file.size / (1024*1024*1024)).toFixed(2)} GB`;
        
        document.getElementById('fileInput').value = '';
        return;
    }
    
    selectedFile = file;
    const fileInfo = document.getElementById('fileInfo');
    const sizeMB = (file.size / 1024 / 1024).toFixed(2);
    const sizeGB = (file.size / (1024*1024*1024)).toFixed(2);
    const sizeStr = file.size > 1024 * 1024 * 1024 ? `${sizeGB} GB` : `${sizeMB} MB`;
    
    fileInfo.textContent = `${file.name} (${sizeStr})`;
    fileInfo.style.color = 'var(--ok)';
    inspectFile(file);
}

async function startProcessing() {
    if (!selectedMethod || !selectedFile || !selectedDeviceType || preflightBusy || preflightBlocked) return;
    if (selectedProfileDevice?.phaseCenterValidUntil && new Date(selectedProfileDevice.phaseCenterValidUntil) <= new Date()) {
        applySavedDevice(); return;
    }
    
    const token = localStorage.getItem('token');
    if (!token) {
        window.location.href = '/login';
        return;
    }
    
    const btn = document.getElementById('processBtn');
    const statusDiv = document.getElementById('status');

    btn.disabled = true;
    btn.classList.add('uploading');
    statusDiv.style.display = 'none';

    setBtnProgress(0);
    setBtnText('Загрузка 0%');

    // Without a browser byte count, show an indeterminate state, never invented percentages.
    setBtnText('Загрузка файла…');
    const config = { ...methodDetails[selectedMethod].config, deviceType: selectedDeviceType };
    if (selectedProfileDevice && selectedDeviceType === 'gnss') {
        config.antennaSource = 'profile';
        config.antennaType = selectedProfileDevice.antennaName;
        config.antennaDeltaE = selectedProfileDevice.antennaE;
        config.antennaDeltaN = selectedProfileDevice.antennaN;
        config.antennaDeltaU = selectedProfileDevice.antennaU;
    }
    if (selectedDeviceType === 'mobile') {
        config.antennaType = document.getElementById('mobileAntennaType').value.trim();
        config.antennaDeltaE = parseFloat(document.getElementById('mobileE').value) || 0;
        config.antennaDeltaN = parseFloat(document.getElementById('mobileN').value) || 0;
        config.antennaDeltaU = parseFloat(document.getElementById('mobileU').value) || 0;
    }
    const formData = new FormData();
    formData.append('config', JSON.stringify(config));
    formData.append('file', selectedFile);

    // XHR нужен для upload.onprogress — fetch его не поддерживает
    await new Promise((resolve) => {
        const xhr = new XMLHttpRequest();

        xhr.upload.onprogress = (e) => {
            if (!e.lengthComputable) return;
            const pct = Math.round((e.loaded / e.total) * 100);
            setBtnProgress(pct);
            setBtnText(`Загрузка ${pct}%`);
        };

        xhr.onload = () => {
            setBtnProgress(100);
            btn.classList.remove('uploading');
            let data = {};
            try { data = JSON.parse(xhr.responseText); } catch {}

            if (xhr.status >= 200 && xhr.status < 300) {
                setBtnText('✅ Отправлено');
                statusDiv.className = 'status-message status-success';
                statusDiv.style.display = 'block';
                statusDiv.innerHTML = `
                    ✅ Обработка запущена!<br>
                    <a href="/profile#task-${encodeURIComponent(data.taskId)}">Открыть состояние обработки →</a><br>
                    Можно закрыть страницу — обработка продолжится на сервере.
                `;
                selectedFile = null;
                inspectedHeader = null;
                document.getElementById('filePreview').hidden = true;
                document.getElementById('fileInput').value = '';
                selectedProfileDevice = null;
                document.getElementById('savedDevice').value = '';
                selectedMethod = null;
                selectedDeviceType = null;
                document.getElementById('fileInfo').innerHTML = 'Поддерживаются форматы: RINEX (.obs, .rnx, .crx, .YYo, .YYd), сжатые (.gz)';
                document.getElementById('fileInfo').style.color = '#718096';
                document.querySelectorAll('.method-option[data-method]').forEach(opt => opt.classList.remove('selected'));
                document.querySelectorAll('.device-option').forEach(el => el.classList.remove('selected'));
                document.getElementById('methodDetail').innerHTML = `
                    <h4>👆 Выберите метод обработки</h4>
                    <p>Нажмите на один из методов выше, чтобы увидеть подробное описание</p>
                `;
                document.getElementById('deviceSection').style.display = 'none';
                document.getElementById('mobileAntennaSection').style.display = 'none';
                updateButtonState();
                setTimeout(() => { window.location.href = '/profile#task-' + encodeURIComponent(data.taskId); }, 1200);
            } else {
                setBtnProgress(0);
                statusDiv.className = 'status-message status-error';
                statusDiv.style.display = 'block';
                statusDiv.textContent = data.error || '❌ Ошибка запуска обработки';
                btn.disabled = false;
                updateButtonState();
            }
            resolve();
        };

        xhr.onerror = () => {
            btn.classList.remove('uploading');
            setBtnProgress(0);
            statusDiv.className = 'status-message status-error';
            statusDiv.style.display = 'block';
            statusDiv.textContent = '❌ Ошибка соединения с сервером';
            btn.disabled = false;
            updateButtonState();
            resolve();
        };

        xhr.open('POST', '/api/measurements/process');
        xhr.setRequestHeader('Authorization', `Bearer ${token}`);
        xhr.send(formData);
    });
}

// Инициализация
checkAuth();
// Only header bytes are inspected, in this tab; no extra server copy is created.
async function readHeader(file) {
    if (!file.name.toLowerCase().endsWith('.gz')) return file.slice(0, 256 * 1024).text();
    if (typeof DecompressionStream === 'undefined') return null;
    const reader = file.stream().pipeThrough(new DecompressionStream('gzip')).getReader();
    const decoder = new TextDecoder();
    let text = '', bytes = 0;
    try {
        while (bytes < 256 * 1024) {
            const {done, value} = await reader.read();
            if (done) break;
            text += decoder.decode(value.subarray(0, 256 * 1024 - bytes), {stream:true});
            bytes += value.byteLength;
            if (text.includes('END OF HEADER')) break;
        }
        return text;
    } finally { await reader.cancel().catch(() => {}); }
}

async function inspectFile(file) {
    const panel = document.getElementById('filePreview');
    panel.hidden = false;
    panel.textContent = 'Проверяем заголовок файла в браузере…';
    preflightBusy = true; preflightBlocked = false; inspectedHeader = null;
    updateButtonState();
    try {
        const text = await readHeader(file);
        if (selectedFile !== file) return;
        if (text === null) {
            panel.textContent = 'Этот браузер не поддерживает проверку gzip до загрузки. Формат будет проверен на сервере.';
            return;
        }
        const info = Workflow.parseRinexHeader(text);
        inspectedHeader = info;
        if (!info.valid) {
            preflightBlocked = true;
            panel.textContent = 'Не найден заголовок RINEX наблюдений. Выберите файл наблюдений (.obs, .rnx, .crx, .YYo, .YYd), а не навигационный файл.';
            return;
        }
        const duration = info.first && info.last ? info.last.seconds - info.first.seconds : null;
        panel.innerHTML = `<h3>Файл проверен: RINEX ${Workflow.escape(info.version)}</h3>
          <div class="workflow-grid">
           <div>Начало<br><strong>${Workflow.escape(info.first?.text || 'Не указано в заголовке')}</strong></div>
           <div>Конец<br><strong>${Workflow.escape(info.last?.text || 'Не указан в заголовке')}</strong></div>
           <div>Длительность<br><strong>${duration !== null && duration >= 0 ? (duration/60).toFixed(1) + ' мин' : 'Уточнится при обработке'}</strong></div>
           <div>Антенна<br><strong>${Workflow.escape(info.antenna || 'Не указана')}</strong></div>
          </div>
          <p>Диапазоны наблюдений: ${Workflow.escape(info.bands.join(', ') || 'не определены')}. Шкала времени: ${Workflow.escape(info.first?.system || 'не указана')}.</p>
          <p class="workflow-note">Это проверка заголовка, а не качества всех наблюдений. ${info.complete ? '' : 'Заголовок больше лимита предпросмотра; проверка частичная.'}</p>`;
    } catch {
        if (selectedFile !== file) return;
        preflightBlocked = true;
        panel.textContent = 'Не удалось прочитать файл. Проверьте, что архив gzip не повреждён, или выберите распакованный RINEX.';
    } finally {
        if (selectedFile === file) { preflightBusy = false; updateButtonState(); }
    }
}

async function loadSavedDevices() {
    try {
        const r = await fetch('/api/devices', { headers: { Authorization: 'Bearer ' + localStorage.getItem('token') } });
        if (!r.ok) throw new Error();
        const devices = await r.json();
        // Keep only processing settings in this tab, not connection credentials.
        savedDevices = devices.map(d => ({ id:d.id, name:d.name, deviceType:d.deviceType, antennaName:d.antennaName, antennaE:d.antennaE, antennaN:d.antennaN, antennaU:d.antennaU, phaseCenterValidUntil:d.phaseCenterValidUntil, phaseCenterMethod:d.phaseCenterMethod }));
        const select = document.getElementById('savedDevice');
        savedDevices.forEach(d => select.add(new Option(d.name, String(d.id))));
    } catch { document.getElementById('deviceSource').textContent = 'Устройства не удалось загрузить. Можно указать параметры вручную.'; }
}

function applySavedDevice() {
    const device = savedDevices.find(d => String(d.id) === document.getElementById('savedDevice').value);
    selectedProfileDevice = null;
    document.getElementById('deviceValidity').textContent = '';
    if (!device) {
        document.getElementById('deviceSource').textContent = 'Источник: ручные параметры для мобильного устройства или заголовок RINEX для приёмника.';
        updateButtonState(); return;
    }
    selectDeviceType(device.deviceType === 'gnss_receiver' ? 'gnss' : 'mobile');
    const expired = device.phaseCenterValidUntil && new Date(device.phaseCenterValidUntil).getTime() <= Date.now();
    if (expired) {
        document.getElementById('deviceValidity').textContent = 'Срок калибровки истёк. Старые поправки не подставлены: выполните калибровку или задайте актуальные ENU вручную.';
        ['E','N','U'].forEach(axis => document.getElementById('mobile'+axis).value = '');
        document.getElementById('mobileAntennaType').value = '';
        document.getElementById('deviceSource').textContent = 'Источник: ручной ввод, калибровка устройства просрочена.';
        checkMobileWarning(); updateButtonState(); return;
    }
    selectedProfileDevice = device;
    document.getElementById('mobileAntennaType').value = device.antennaName || '';
    ['E','N','U'].forEach(axis => document.getElementById('mobile'+axis).value = device['antenna'+axis] || 0);
    document.getElementById('deviceSource').textContent = `Источник: профиль «${device.name}». Антенна: ${device.antennaName || 'UNKNOWN'}. E: ${device.antennaE || 0}, N: ${device.antennaN || 0}, U: ${device.antennaU || 0} м.`;
    if (device.phaseCenterValidUntil) document.getElementById('deviceValidity').textContent = 'Параметры действительны до ' + new Date(device.phaseCenterValidUntil).toLocaleString('ru-RU') + '. Учитывайте ориентацию устройства при калибровке.';
    checkMobileWarning(); updateButtonState();
}
loadSavedDevices();

// Transfer only settings from the originating tab, without storing task history.
if (window.opener) {
    const receiveSettings = event => {
        if (event.origin !== location.origin || event.source !== window.opener || event.data?.type !== 'processing-settings') return;
        const c = event.data.config || {};
        const method = c.method === 'ppp' ? (c.mode === 'static' ? 'ppp-static' : 'ppp-kinematic') : c.method === 'single' ? 'single' : null;
        if (!method) return;
        selectMethod(method);
        selectDeviceType(c.deviceType === 'mobile' ? 'mobile' : 'gnss');
        // Keep all supported numeric/enum solver settings, without IDs or file paths.
        for (const key of Object.keys(methodDetails[method].config)) {
            if (typeof c[key] === typeof methodDetails[method].config[key]) methodDetails[method].config[key] = c[key];
        }
        document.getElementById('mobileAntennaType').value = c.antennaType || '';
        ['E','N','U'].forEach(axis => document.getElementById('mobile'+axis).value = Number(c['antennaDelta'+axis]) || 0);
        if (c.antennaSource === 'profile' && c.deviceType !== 'mobile') {
            selectedProfileDevice = {name:'Параметры предыдущего расчёта', deviceType:'gnss_receiver', antennaName:c.antennaType, antennaE:c.antennaDeltaE, antennaN:c.antennaDeltaN, antennaU:c.antennaDeltaU};
        }
        document.getElementById('deviceSource').textContent = 'Настройки перенесены из предыдущего расчёта. Проверьте их актуальность и выберите файл заново.';
        checkMobileWarning(); updateButtonState();
        window.removeEventListener('message', receiveSettings);
    };
    window.addEventListener('message', receiveSettings);
    window.opener.postMessage({type:'processing-ready'}, location.origin);
}

['mobileAntennaType','mobileE','mobileN','mobileU'].forEach(id => {
    document.getElementById(id).addEventListener('input', () => {
        selectedProfileDevice = null;
        document.getElementById('savedDevice').value = '';
        document.getElementById('deviceSource').textContent = 'Источник: ручной ввод параметров ENU.';
        document.getElementById('deviceValidity').textContent = '';
    });
});
