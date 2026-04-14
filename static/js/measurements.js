
let selectedMethod = null;
let selectedFile = null;

const methodDetails = {
    single: {
        title: "📍 Абсолютный метод (SPP)",
        description: "Одиночное точечное позиционирование с использованием псевдодальностей и широковещательных эфемерид.",
        features: [
            "✅ Не требует базовой станции",
            "✅ Быстрая обработка (несколько секунд)",
            "✅ Подходит для навигации"
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
        title: "🎯 PPP-AR Кинематика",
        description: "Прецизионное точечное позиционирование с разрешением неоднозначностей для движущихся объектов.",
        features: [
            "✅ Не требует базовой станции",
            "✅ Разрешение целочисленных неоднозначностей (AR)",
            "✅ Для движущихся объектов",
            "⚠️ Требует 15-30 минут для инициализации"
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
        title: "🎯 PPP-AR Статика",
        description: "Прецизионное точечное позиционирование с разрешением неоднозначностей для неподвижных объектов.",
        features: [
            "✅ Не требует базовой станции",
            "✅ Разрешение целочисленных неоднозначностей (AR)",
            "✅ Для неподвижных объектов",
            "✅ Накопление решения со временем"
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
        <ul>${detail.features.map(f => `<li>${f}</li>`).join('')}</ul>
    `;
    
    updateButtonState();
}

function updateButtonState() {
    const btn = document.getElementById('processBtn');
    if (selectedMethod && selectedFile) {
        btn.disabled = false;
        btn.textContent = '🚀 Запустить обработку';
    } else if (selectedMethod && !selectedFile) {
        btn.disabled = true;
        btn.textContent = '📁 Выберите файл наблюдений';
    } else if (!selectedMethod && selectedFile) {
        btn.disabled = true;
        btn.textContent = '📍 Выберите метод обработки';
    } else {
        btn.disabled = true;
        btn.textContent = 'Выберите метод и файл';
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
    
    if (file.size > maxSize) {
        const statusDiv = document.getElementById('status');
        statusDiv.className = 'status-message status-error';
        statusDiv.style.display = 'block';
        statusDiv.textContent = `❌ Файл слишком большой. Максимальный размер: 1 GB. Ваш файл: ${(file.size / (1024*1024*1024)).toFixed(2)} GB`;
        
        document.getElementById('fileInput').value = '';
        return;
    }
    
    selectedFile = file;
    const fileInfo = document.getElementById('fileInfo');
    const sizeMB = (file.size / 1024 / 1024).toFixed(2);
    const sizeGB = (file.size / (1024*1024*1024)).toFixed(2);
    const sizeStr = file.size > 1024 * 1024 * 1024 ? `${sizeGB} GB` : `${sizeMB} MB`;
    
    fileInfo.innerHTML = `✅ ${file.name} (${sizeStr})`;
    fileInfo.style.color = '#2e7d32';
    updateButtonState();
}

async function startProcessing() {
    if (!selectedMethod || !selectedFile) return;
    
    const token = localStorage.getItem('token');
    if (!token) {
        window.location.href = '/login';
        return;
    }
    
    const btn = document.getElementById('processBtn');
    const statusDiv = document.getElementById('status');
    
    btn.disabled = true;
    btn.textContent = '⏳ Обработка...';
    
    statusDiv.style.display = 'none';
    
    const config = methodDetails[selectedMethod].config;
    const formData = new FormData();
    formData.append('config', JSON.stringify(config));
    formData.append('file', selectedFile);
    
    try {
        const response = await fetch('/api/measurements/process', {
            method: 'POST',
            headers: { 'Authorization': `Bearer ${token}` },
            body: formData
        });
        
        const data = await response.json();
        
        if (response.ok) {
            statusDiv.className = 'status-message status-success';
            statusDiv.style.display = 'block';
            statusDiv.innerHTML = `
                ✅ Обработка запущена!<br>
                ID задачи: <strong>${data.taskId}</strong><br>
                Результат появится в истории личного кабинета.
            `;
            
            selectedFile = null;
            selectedMethod = null;
            document.getElementById('fileInfo').innerHTML = 'Поддерживаются форматы: RINEX (.obs, .rnx, .crx, .YYd), сжатые (.gz)';
            document.getElementById('fileInfo').style.color = '#718096';
            document.querySelectorAll('.method-option[data-method]').forEach(opt => {
                opt.classList.remove('selected');
            });
            document.getElementById('methodDetail').innerHTML = `
                <h4>👆 Выберите метод обработки</h4>
                <p>Нажмите на один из методов выше, чтобы увидеть подробное описание</p>
            `;
            updateButtonState();
            
            setTimeout(() => { 
                window.location.href = '/profile'; 
            }, 3000);
        } else {
            statusDiv.className = 'status-message status-error';
            statusDiv.style.display = 'block';
            statusDiv.textContent = data.error || '❌ Ошибка запуска обработки';
            btn.disabled = false;
            updateButtonState();
        }
    } catch (error) {
        console.error('Processing error:', error);
        statusDiv.className = 'status-message status-error';
        statusDiv.style.display = 'block';
        statusDiv.textContent = '❌ Ошибка соединения с сервером';
        btn.disabled = false;
        updateButtonState();
    }
}

// Инициализация
checkAuth();