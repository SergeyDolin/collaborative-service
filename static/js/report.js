/* Report calculations are transient: no localStorage, uploads or additional history. */
(function(root) {
'use strict';
const esc = v => String(v ?? '—').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const finite = v => typeof v === 'number' && Number.isFinite(v);
const fmt = (v, n=3) => finite(v) ? v.toLocaleString('ru-RU', {maximumFractionDigits:n, minimumFractionDigits:n}) : '—';
const qualities = {0:['Нет решения','#64748b'],1:['FIX','#15803d'],2:['FLOAT','#d97706'],3:['SBAS','#0891b2'],4:['DGPS','#7c3aed'],5:['SPP','#dc2626'],6:['PPP','#2563eb'],7:['DR','#9333ea']};
const quality = q => qualities[q] || [`Q=${q}`, '#475569'];
function stats(values) {
 const a = values.filter(finite).sort((a,b)=>a-b), n = a.length;
 if (!n) return {n:0};
 const mean = a.reduce((sum,v)=>sum+v,0)/n;
 const percentile = p => { const i=(n-1)*p, j=Math.floor(i); return a[j]+(a[Math.min(j+1,n-1)]-a[j])*(i-j); };
 return {n,min:a[0],max:a[n-1],mean,median:percentile(.5),p95:percentile(.95),sd:n>1?Math.sqrt(a.reduce((s,v)=>s+(v-mean)**2,0)/(n-1)):null};
}
function epoch(time) {
 if (!/^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}(\.\d+)?$/.test(time || '')) return null;
 const t = Date.parse(time.replaceAll('/','-').replace(' ','T')+'Z');
 return Number.isFinite(t) ? t/1000 : null; // Relative intervals only; never relabel GPST as UTC.
}
function ecef(p) {
 const b=p.lat*Math.PI/180, l=p.lon*Math.PI/180, e2=6.69437999014e-3;
 const n=6378137/Math.sqrt(1-e2*Math.sin(b)**2);
 return [(n+p.h)*Math.cos(b)*Math.cos(l),(n+p.h)*Math.cos(b)*Math.sin(l),(n*(1-e2)+p.h)*Math.sin(b)];
}
function analyze(input) {
 const points=(input || []).filter(p=>finite(p.lat)&&Math.abs(p.lat)<=90&&finite(p.lon)&&Math.abs(p.lon)<=180&&finite(p.h));
 if (!points.length) return {points,counts:{}};
 const origin=points[0], xyz=ecef(origin), b=origin.lat*Math.PI/180,l=origin.lon*Math.PI/180;
 const local=points.map(p=>{const v=ecef(p).map((v,i)=>v-xyz[i]); return {...p,e:-Math.sin(l)*v[0]+Math.cos(l)*v[1],n:-Math.sin(b)*Math.cos(l)*v[0]-Math.sin(b)*Math.sin(l)*v[1]+Math.cos(b)*v[2],u:Math.cos(b)*Math.cos(l)*v[0]+Math.cos(b)*Math.sin(l)*v[1]+Math.sin(b)*v[2],t:epoch(p.time)};});
 const counts={}, intervals=[];
 let transitions=0, nonIncreasing=0;
 local.forEach((p,i)=>{counts[p.q]=(counts[p.q]||0)+1;if(i){if(p.q!==local[i-1].q)transitions++;if(finite(p.t)&&finite(local[i-1].t)){const dt=p.t-local[i-1].t;if(dt>0)intervals.push(dt);else nonIncreasing++;}}});
 const cadence=stats(intervals), gaps=intervals.filter(v=>v>cadence.median*1.5);
 const first=local[0],last=local[local.length-1];
 return {points:local,counts,transitions,nonIncreasing,cadence,gaps:stats(gaps),duration:finite(first.t)&&finite(last.t)&&last.t>=first.t?last.t-first.t:null,
  height:stats(local.map(p=>p.h)),east:stats(local.map(p=>p.e)),north:stats(local.map(p=>p.n)),up:stats(local.map(p=>p.u))};
}
const sample = (a,limit=1400) => a.length<=limit?a:Array.from({length:limit},(_,i)=>a[Math.round(i*(a.length-1)/(limit-1))]);
function chart(points, keys, title, unit, integer=false) {
 const values=points.flatMap(p=>keys.map(k=>p[k[0]])).filter(finite); if(!values.length)return '';
 const bounds=stats(values), lo=integer?Math.floor(bounds.min):bounds.min, span=bounds.max-lo||1, W=760,H=210,L=85,T=18,B=36;
 const useTime=points.every(p=>finite(p.t)) && points.every((p,i)=>!i||p.t>points[i-1].t) && points.length>1;
 const x=(p,i)=>L+(useTime?(p.t-points[0].t)/(points.at(-1).t-points[0].t):i/Math.max(1,points.length-1))*(W-L-20);
 const y=v=>T+(1-(v-lo)/span)*(H-T-B);
 let svg='';
 const ticks = integer ? [...new Set(Array.from({length:5},(_,i)=>Math.round(lo+span*i/4)))] : Array.from({length:5},(_,i)=>lo+span*i/4);
 for(const v of ticks){svg+=`<path d="M${L} ${y(v)}H${W-20}" stroke="#e2e8f0"/><text x="${L-8}" y="${y(v)+4}" text-anchor="end">${fmt(v,integer?0:2)}</text>`;}
 keys.forEach(([key,label,color])=>{
  const a=sample(points.map((p,i)=>({p,i}))); let segment=false,path='';
  a.forEach(({p,i})=>{if(!finite(p[key])){segment=false;return;}path+=segment?(integer?`H${x(p,i).toFixed(2)} V${y(p[key]).toFixed(2)} `:`L${x(p,i).toFixed(2)},${y(p[key]).toFixed(2)} `):`M${x(p,i).toFixed(2)},${y(p[key]).toFixed(2)} `;segment=true;});
  svg+=`<path d="${path}" stroke="${color}" fill="none" stroke-width="1.5"/>`;
  if(points.length===1&&finite(points[0][key]))svg+=`<circle cx="${x(points[0],0)}" cy="${y(points[0][key])}" r="3" fill="${color}"/>`;
 });
 for(let i=0;i<=4;i++){const index=Math.round(i*(points.length-1)/4), p=points[index];svg+=`<text x="${x(p,index)}" y="${H-16}" text-anchor="middle">${useTime?fmt(p.t-points[0].t,1):index+1}</text>`;}
 return `<section><h2>${esc(title)}</h2><p class="legend">${esc(unit)} · ${keys.map(k=>`<span style="color:${k[2]}">${esc(k[1])}</span>`).join(' · ')} · ${useTime?'Время, с':'Эпоха'}</p><svg role="img" aria-label="${esc(title)}" viewBox="0 0 ${W} ${H}">${svg}</svg></section>`;
}
function plan(points) {
 if(!points.length)return '';
 const es=stats(points.map(p=>p.e)),ns=stats(points.map(p=>p.n));
 const scale=Math.min(640/Math.max(es.max-es.min,.01),290/Math.max(ns.max-ns.min,.01));
 const x=p=>380+(p.e-(es.max+es.min)/2)*scale,y=p=>175-(p.n-(ns.max+ns.min)/2)*scale;
 const pts=sample(points), path=pts.map(p=>`${x(p).toFixed(2)},${y(p).toFixed(2)}`).join(' ');
 return `<section><h2>Плановая траектория / разброс координат</h2><p class="legend">Север ↑ · Восток → · E, N от первой эпохи, м</p><svg role="img" aria-label="Плановая траектория" viewBox="0 0 760 375"><polyline points="${path}" fill="none" stroke="#cbd5e1"/>${pts.map(p=>`<circle cx="${x(p)}" cy="${y(p)}" r="2" fill="${quality(p.q)[1]}"/>`).join('')}<text x="12" y="360">Размах E: ${fmt(es.max-es.min)} м · N: ${fmt(ns.max-ns.min)} м</text><text x="${x(points[0])+5}" y="${y(points[0])-7}">Начало</text><text x="${x(points.at(-1))+5}" y="${y(points.at(-1))+15}">Конец</text></svg></section>`;
}
// Runs inside the isolated map frame, only after the user opens the map.
async function mapRuntime(data) {
 const status = document.getElementById('status');
 try {
  await Promise.all([
   new Promise((resolve,reject)=>{const link=document.createElement('link');link.rel='stylesheet';link.href='https://unpkg.com/leaflet@1.9.4/dist/leaflet.css';link.onload=resolve;link.onerror=reject;document.head.append(link);}),
   new Promise((resolve,reject)=>{const script=document.createElement('script');script.src='https://unpkg.com/leaflet@1.9.4/dist/leaflet.js';script.onload=resolve;script.onerror=reject;document.head.append(script);})
  ]);
  const map=L.map('map',{preferCanvas:true,maxZoom:22});
  const tiles=L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png',{
   maxNativeZoom:19,maxZoom:22,attribution:'© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap</a>'
  }).addTo(map);
  tiles.on('tileerror',()=>{status.textContent='Подложка карты недоступна. Точки результата показаны.';});
  const epochs=L.layerGroup().addTo(map), result=L.layerGroup().addTo(map), bounds=L.latLngBounds([]);
  const popup=(p,label)=>{const el=document.createElement('div');el.style.whiteSpace='pre-line';el.textContent=label+'\n'+(p.time||'')+'\nB = '+p.lat.toFixed(9)+'°\nL = '+p.lon.toFixed(9)+'°\nH = '+p.h.toFixed(4)+' м';return el;};
  for(let i=0;i<data.points.length;i++) {
   const p=data.points[i], ll=[p.lat,p.lon]; bounds.extend(ll);
   L.circleMarker(ll,{radius:3,color:'#2563eb',weight:1,fillOpacity:.7}).bindPopup(()=>popup(p,'Эпоха '+(i+1))).addTo(epochs);
   if(i>0 && i%1000===0)await new Promise(resolve=>setTimeout(resolve,0));
  }
  if(data.result){const p=data.result;bounds.extend([p.lat,p.lon]);L.circleMarker([p.lat,p.lon],{radius:7,color:'#dc2626',weight:3,fillOpacity:0}).bindPopup(()=>popup(p,'Координаты результата')).addTo(result);}
  if(bounds.isValid())map.fitBounds(bounds,{padding:[24,24],maxZoom:19});
  L.control.layers(null,{'Точки эпох':epochs,'Координаты результата':result},{collapsed:false}).addTo(map);
  L.control.scale({imperial:false}).addTo(map);
  document.getElementById('fit').onclick=()=>map.fitBounds(bounds,{padding:[24,24],maxZoom:19});
  status.textContent='Точки эпох: '+data.points.length;
 } catch {status.textContent='Не удалось загрузить карту. Проверьте соединение и сформируйте отчёт повторно.';}
}
function mapDocument(points, result) {
 const data={points:points.map(({lat,lon,h,time})=>({lat,lon,h,time})),result};
 const json=JSON.stringify(data).replace(/</g,'\\u003c');
 return `<!doctype html><html lang="ru"><head><meta charset="utf-8"><style>html,body{height:100%;margin:0;font:14px Arial}body{display:flex;flex-direction:column}header{padding:8px;display:flex;gap:12px;align-items:center}button{font:inherit}#map{flex:1;min-height:0}</style></head><body><header><button id="fit">Все точки</button><span id="status">Загрузка карты…</span></header><div id="map"></div><script>(${mapRuntime.toString()})(${json});<\/script></body></html>`;
}
function render(task, td, unavailable='') {
 const a=analyze(td?.points), ps=a.points, r=task.result||{};
 const fields=(r.lastSolutionLine||'').trim().split(/\s+/);
 const coord=fields.length>=7?{lat:Number(fields[2]),lon:Number(fields[3]),h:Number(fields[4])}:{lat:r.latitude,lon:r.longitude,h:r.height};
 const valid=finite(coord.lat)&&Math.abs(coord.lat)<=90&&finite(coord.lon)&&Math.abs(coord.lon)<=180&&finite(coord.h);
 const row=(label,value)=>`<tr><th>${esc(label)}</th><td>${esc(value)}</td></tr>`;
 const xyz=valid?ecef(coord):null;
 const mapPoints=ps.filter(p=>Math.abs(p.lat)<85.05112878);
 const mapResult=valid&&Math.abs(coord.lat)<85.05112878?coord:null;
 return `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Отчёт ГНСС — ${esc(task.filename)}</title><style>
*{box-sizing:border-box}body{margin:0;background:#edf2f7;color:#172b40;font:15px/1.5 Arial,sans-serif}main{max-width:1000px;margin:24px auto;background:white;padding:36px;border-radius:12px}h1{font-size:28px;margin:0}h2{font-size:19px;margin:0 0 10px}section{margin:26px 0;break-inside:avoid}p{margin:8px 0}.meta,.legend{font-size:inherit;color:#526478}.cards{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}.card{padding:16px;background:#eff6ff;border-radius:8px}.card strong{display:block;font-size:21px}table{width:100%;border-collapse:collapse;font-size:13px}th,td{padding:8px;text-align:left;border-bottom:1px solid #e2e8f0}th{font-weight:500}thead{background:#eff6ff}svg{display:block;width:100%;height:auto;background:#f8fafc;border-radius:8px}svg text{font:11px Arial;fill:#526478}button{font:inherit;padding:9px 15px;border:1px solid #b8c7d8;background:white;border-radius:6px;cursor:pointer}.tools{display:flex;gap:10px;margin-bottom:20px}.scroll{overflow:auto}iframe{width:100%;height:400px;border:0}@media(max-width:600px){main{margin:0;padding:16px}.cards{grid-template-columns:1fr}h1{font-size:23px}}@media print{@page{margin:14mm}body{background:white;font-size:11pt}main{margin:0;padding:0;max-width:none}.tools,.map-controls{display:none}section{break-inside:avoid}*{print-color-adjust:exact;-webkit-print-color-adjust:exact}.scroll{overflow:visible}table{font-size:9pt}iframe{height:300px}}
</style></head><body><main><div class="tools"><button onclick="window.print()">Печать / сохранить PDF</button></div><h1>Отчёт об обработке ГНСС-наблюдений</h1><p>${esc(task.filename)}</p><p class="meta">Задача ${esc(task.id)} · сформирован ${esc(new Date().toLocaleString('ru-RU'))}</p>
<section><h2>Координаты результата</h2><table>${row('Широта B, °',fmt(coord.lat,9))}${row('Долгота L, °',fmt(coord.lon,9))}${row('Эллипсоидальная высота H, м',fmt(coord.h,4))}${xyz?row('ECEF X / Y / Z (WGS 84), м',xyz.map(v=>fmt(v,4)).join(' / ')):''}</table></section>
<section><h2>Карта местности</h2>${mapResult||mapPoints.length?`<div class="map-controls"><button id="load-map" data-map="${esc(mapDocument(mapPoints,mapResult))}" onclick="const f=document.createElement('iframe');f.title='Карта точек результата';f.srcdoc=this.dataset.map;document.getElementById('map').append(f);this.disabled=true;this.textContent='Карта открыта'">Загрузить карту OpenStreetMap</button></div><div id="map"></div>`:'<p>Карта недоступна для этих координат.</p>'}</section>
${unavailable?`<p role="status">${esc(unavailable)}</p>`:''}
${ps.length?`<section><h2>Период наблюдений</h2><table>${row('Число эпох',ps.length)}${row('Длительность, с',fmt(a.duration,1))}${row('Первая эпоха',ps[0].time)}${row('Последняя эпоха',ps.at(-1).time)}${row('Шкала времени',td?.timeSystem||'не указана')}${row('Медианный интервал, с',fmt(a.cadence.median))}</table></section>`:''}
${plan(ps)}${chart(ps,[['h','Высота','#2563eb']],'Высота по эпохам','м')}${chart(ps,[['ns','Спутники','#15803d']],'Число спутников в решении','шт.',true)}${chart(ps,[['sdn','Север (σN)','#2563eb'],['sde','Восток (σE)','#15803d'],['sdu','Высота (σU)','#d97706']],'Стандартные отклонения координат σN, σE, σU','м')}${chart(ps,[['e','Восток','#2563eb'],['n','Север','#15803d'],['u','Вверх','#d97706']],'Изменение координат относительно первой эпохи','м')}
</main></body></html>`;
}
const api={stats,epoch,analyze,render,quality,chart,mapDocument};
if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GNSSReport=api;
})(typeof window!=='undefined'?window:globalThis);
