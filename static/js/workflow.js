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
        const info = { valid: false, complete: false, version: '', antenna: '', first: null, last: null, bands: [], systems: [], phaseBands: {}, observationTypes: {}, fileSystem: '', observationsKnown: false };
        let currentSystem = '';
        for (const line of text.split(/\r?\n/)) {
            const label = line.slice(60).trim();
            if (label === 'RINEX VERSION / TYPE') {
                info.valid = line[20] === 'O' || line.slice(20,40).includes('OBSERVATION');
                info.version = line.slice(0,9).trim();
                info.fileSystem = line.slice(40,41).trim();
            }
            if (label === 'ANT # / TYPE') info.antenna = line.slice(20,40).trim();
            if (label === 'TIME OF FIRST OBS') info.first = rinexTime(line);
            if (label === 'TIME OF LAST OBS') info.last = rinexTime(line);
            if (label === 'SYS / # / OBS TYPES' || label === '# / TYPES OF OBSERV') {
                info.observationsKnown = true;
                if (label.startsWith('SYS')) currentSystem = line[0].trim() || currentSystem;
                else currentSystem = 'RINEX2';
                if (currentSystem && !info.systems.includes(currentSystem)) info.systems.push(currentSystem);
                for (const m of line.slice(0,60).matchAll(/\b([PCLDS])([1-9])[A-Z]?\b/g)) {
                    const types = info.observationTypes[currentSystem] ||= [];
                    if (!types.includes(m[0])) types.push(m[0]);
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
    // Band numbers are constellation-specific (IGS RINEX 3.04, tables 4–10).
    function signalSummary(info) {
        const names = { G: 'GPS', R: 'ГЛОНАСС', E: 'Galileo', C: 'BeiDou', J: 'QZSS', I: 'NavIC (IRNSS)', S: 'SBAS' };
        const bands = {
            G: {1:'L1',2:'L2',5:'L5'}, R: {1:'G1 (L1)',2:'G2 (L2)',3:'G3 (L3)',4:'G1a',6:'G2a'},
            E: {1:'E1',5:'E5a',6:'E6',7:'E5b',8:'E5 (E5a + E5b)'},
            C: {1:'B1',2:'B1I',5:'B2a',6:'B3',7:'B2',8:'B2 (B2a + B2b)'},
            J: {1:'L1',2:'L2',5:'L5',6:'L6'}, I: {1:'L1',5:'L5',9:'S'}, S: {1:'L1',5:'L5'}
        };
        return Object.entries(info.observationTypes).map(([key, types]) => {
            const system = key === 'RINEX2' ? (info.fileSystem || 'G') : key;
            if (key === 'RINEX2' && !names[system]) return {
                system: 'RINEX 2 — общие типы наблюдений',
                signals: types.join(', ') + ' (в заголовке не разделены по спутниковым системам)'
            };
            const signals = types.slice().sort((a,b) => a[1].localeCompare(b[1])).map(code => {
                const band = code[1], tracking = code[2];
                if (system === 'C') {
                    if (band === '1' && info.version === '3.02') return 'B1I';
                    if (band === '1' && 'DPX'.includes(tracking)) return 'B1C';
                    if (band === '1' && tracking === 'A') return 'B1A';
                    if (band === '6' && 'IQX'.includes(tracking)) return 'B3I';
                    if (band === '6' && tracking === 'A') return 'B3A';
                    if (band === '7' && 'IQX'.includes(tracking)) return 'B2I';
                    if (band === '7' && 'DPZ'.includes(tracking)) return 'B2b';
                }
                return bands[system]?.[band] || `неизвестный сигнал (${code})`;
            });
            return { system: names[system] || `Система ${system}`, signals: [...new Set(signals)].join(', ') };
        });
    }
    function preflightAdvice(info, method) {
        if (!info) return '';
        if (method?.startsWith('ppp') && info.complete && info.observationsKnown && !Object.values(info.phaseBands).some(b => b.length >= 2)) return 'В заголовке не найдены фазовые наблюдения на двух частотах одной системы. Для выбранного PPP нужны подходящие многочастотные данные. Проверьте запись или выберите SPP.';
        if (method?.startsWith('ppp') && info.first && Date.now()/1000 - info.first.seconds < 86400) return 'Наблюдения за последние сутки: точные орбиты и часы могут быть ещё недоступны. При отказе повторите запуск позже.';
        return '';
    }
    function qualityText(q, fixRate) {
        const label = ({1:'FIX — фазовые неоднозначности разрешены',2:'FLOAT — неоднозначности не зафиксированы',4:'DGPS — дифференциальное решение',5:'SPP — абсолютное решение',6:'PPP'})[q] || 'Тип решения не определён';
        return label
    }
    const api = { stages, escape, parseRinexHeader, signalSummary, preflightAdvice, qualityText };
    if (typeof module !== 'undefined' && module.exports) module.exports = api;
    else root.Workflow = api;
})(typeof window !== 'undefined' ? window : globalThis);
