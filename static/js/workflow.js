/* Shared, side-effect-free display and RINEX header helpers. */
(function(root) {
    const stages = { checking: 'Проверка файла', converting: 'Подготовка RINEX', products: 'Загрузка орбит и часов', configuring: 'Подготовка расчёта', calculating: 'Расчёт координат', saving: 'Подготовка результата' };
    function escape(value) { return String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
    function rinexTime(line) {
        const f = line.slice(0, 60).trim().split(/\s+/);
        if (f.length < 6) return null;
        const n = f.slice(0, 6).map(Number);
        if (!n.every(Number.isFinite) || n[0] < 1980 || n[1] < 1 || n[1] > 12 || n[2] < 1 || n[2] > 31) return null;
        return { text: `${n[0]}-${String(n[1]).padStart(2,'0')}-${String(n[2]).padStart(2,'0')} ${String(n[3]).padStart(2,'0')}:${String(n[4]).padStart(2,'0')}:${String(n[5]).padStart(2,'0')}`, seconds: Date.UTC(...[n[0], n[1]-1, n[2], n[3], n[4], n[5]]) / 1000, system: f[6] || 'шкала не указана' };
    }
    function parseRinexHeader(text) {
        const info = { valid: false, complete: false, version: '', antenna: '', first: null, last: null, bands: [], systems: [], phaseBands: {}, observationsKnown: false };
        let currentSystem = '';
        for (const line of text.split(/\r?\n/)) {
            const label = line.slice(60).trim();
            if (label === 'RINEX VERSION / TYPE') {
                info.valid = line[20] === 'O' || line.slice(20,40).includes('OBSERVATION');
                info.version = line.slice(0,9).trim();
            }
            if (label === 'ANT # / TYPE') info.antenna = line.slice(20,40).trim();
            if (label === 'TIME OF FIRST OBS') info.first = rinexTime(line);
            if (label === 'TIME OF LAST OBS') info.last = rinexTime(line);
            if (label === 'SYS / # / OBS TYPES' || label === '# / TYPES OF OBSERV') {
                info.observationsKnown = true;
                if (label.startsWith('SYS')) currentSystem = line[0].trim() || currentSystem;
                else currentSystem = 'RINEX2';
                if (currentSystem && !info.systems.includes(currentSystem)) info.systems.push(currentSystem);
                for (const m of line.slice(0,60).matchAll(/\b([CLDS])([1-9])[A-Z]?\b/g)) {
                    if (!info.bands.includes(m[2])) info.bands.push(m[2]);
                    if (m[1] === 'L') {
                        const bands = info.phaseBands[currentSystem] ||= [];
                        if (!bands.includes(m[2])) bands.push(m[2]);
                    }
                }
            }
            if (label === 'END OF HEADER') { info.complete = true; break; }
        }
        info.bands.sort();
        return info;
    }
    function preflightAdvice(info, method) {
        if (!info) return '';
        if (method?.startsWith('ppp') && info.complete && info.observationsKnown && !Object.values(info.phaseBands).some(b => b.length >= 2)) return 'В заголовке не найдены фазовые наблюдения на двух частотах одной системы. Для выбранного PPP нужны подходящие многочастотные данные. Проверьте запись или выберите SPP.';
        if (method?.startsWith('ppp') && info.first && Date.now()/1000 - info.first.seconds < 86400) return 'Наблюдения за последние сутки: точные орбиты и часы могут быть ещё недоступны. При отказе повторите запуск позже.';
        return '';
    }
    function qualityText(q, fixRate) {
        const label = ({1:'FIX — фазовые неоднозначности разрешены',2:'FLOAT — неоднозначности не зафиксированы',4:'DGPS — дифференциальное решение',5:'SPP — абсолютное решение',6:'PPP — точечное позиционирование'})[q] || 'Тип решения не определён';
        return label + (Number.isFinite(fixRate) ? `. Доля эпох FIX: ${fixRate.toFixed(1)}%.` : '.');
    }
    const api = { stages, escape, parseRinexHeader, preflightAdvice, qualityText };
    if (typeof module !== 'undefined' && module.exports) module.exports = api;
    else root.Workflow = api;
})(typeof window !== 'undefined' ? window : globalThis);
