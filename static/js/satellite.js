(function(root){
'use strict';
function runtime(data){
 const groups=new Map(),epochs=new Map();
 const key=p=>p.id+(p.signal?' / '+p.signal:'');
 for(const p of data.rows){const k=key(p);if(!groups.has(k))groups.set(k,[]);groups.get(k).push(p);if(!epochs.has(p.t))epochs.set(p.t,[]);epochs.get(p.t).push(p);}
 const times=[...epochs.keys()].sort((a,b)=>a-b),select=document.getElementById('satellite'),slider=document.getElementById('epoch');
 const finite=v=>typeof v==='number'&&Number.isFinite(v);
 const fmt=v=>v.toLocaleString('ru-RU',{maximumFractionDigits:2});
 const date=t=>new Date(t*1000).toISOString().replace('T',' ').replace('Z',' GPST');
 for(const k of [...groups.keys()].sort()){const o=document.createElement('option');o.value=k;o.textContent=k;select.append(o);groups.get(k).sort((a,b)=>a.t-b.t);}
 slider.max=times.length-1;slider.value=0;
 const color=id=>({G:'#2563eb',R:'#dc2626',E:'#15803d',C:'#a16207',J:'#7c3aed',I:'#0891b2',S:'#475569'}[id[0]]);
 function sky(){
  const t=times[Number(slider.value)],seen=new Map();document.getElementById('time').textContent=date(t);
  for(const p of epochs.get(t)){if(finite(p.az)&&finite(p.el)&&p.el>=0&&!seen.has(p.id))seen.set(p.id,p);}
  let svg='';for(const el of [0,30,60]){const r=150*(90-el)/90;svg+=`<circle cx="210" cy="180" r="${r}" fill="none" stroke="#cbd5e1"/><text x="215" y="${180-r+14}">${el}°</text>`;}
  svg+='<path d="M60 180H360M210 30V330" stroke="#cbd5e1"/><text x="205" y="20">С</text><text x="374" y="185">В</text><text x="205" y="353">Ю</text><text x="36" y="185">З</text>';
  for(const p of seen.values()){const r=150*(90-p.el)/90,a=p.az*Math.PI/180,x=210+r*Math.sin(a),y=180-r*Math.cos(a);svg+=`<circle cx="${x}" cy="${y}" r="5" fill="${color(p.id)}"/><text x="${x+7}" y="${y-6}">${p.id}</text>`;}
  document.getElementById('sky').innerHTML=svg;
  document.getElementById('sky-state').textContent=seen.size?'Спутников на карте: '+seen.size:'Для этой эпохи нет согласованных орбит и координат решения.';
 }
 function plot(rows,field,title,unit){
  const valid=rows.filter(p=>finite(p[field]));if(!valid.length)return '';
  let low=Infinity,high=-Infinity;for(const p of valid){low=Math.min(low,p[field]);high=Math.max(high,p[field]);}if(low===high){low-=.5;high+=.5;}
  const dt=rows.at(-1).t-rows[0].t||1,x=p=>75+(p.t-rows[0].t)/dt*640,y=v=>160-(v-low)/(high-low)*140;
  let svg='';for(let i=0;i<5;i++){const v=low+(high-low)*i/4;svg+=`<path d="M75 ${y(v)}H715" stroke="#dbe3ec"/><text x="67" y="${y(v)+4}" text-anchor="end">${fmt(v)}</text><text x="${75+i*160}" y="184" text-anchor="middle">${fmt(dt*i/4)}</text>`;}
  for(const p of valid)svg+=`<circle cx="${x(p)}" cy="${y(p[field])}" r="2" fill="#2563eb"><title>${date(p.t)}: ${fmt(p[field])} ${unit}</title></circle>`;
  return `<section><h3>${title}, ${unit}</h3><svg viewBox="0 0 760 200" role="img" aria-label="${title}">${svg}</svg></section>`;
 }
 function update(){const rows=groups.get(select.value);document.getElementById('origin').textContent='Начало графиков: '+date(rows[0].t)+' · время по горизонтали — секунды от начала.';document.getElementById('plots').innerHTML=plot(rows,'cno','C/N₀','дБ·Гц')+plot(rows,'el','Угол места','°')+plot(rows,'az','Азимут','°');sky();}
 select.onchange=update;slider.oninput=sky;update();
}
function documentHTML(report){
 const rows=(report?.rows||[]).filter(p=>/^[GRECIJS]\d{2}$/.test(p.id)&&/^(S\d[A-Z]|)$/.test(p.signal)&&Number.isFinite(p.t)&&p.t>0&&p.t<1e10);
 if(!rows.length)return '';
 const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const json=JSON.stringify({rows}).replace(/</g,'\\u003c');
 return `<!doctype html><html lang="ru"><meta charset="utf-8"><style>body{font:15px/1.5 Arial;color:#172b40;margin:12px}select,input{font:inherit;max-width:100%}svg{width:100%;background:#f8fafc}svg text{font:12px Arial;fill:#526478}#sky{max-width:520px}h3{font-size:16px}section{break-inside:avoid}label{display:block;margin:12px 0}#epoch{width:100%}</style><label>Эпоха <input id="epoch" type="range" min="0" step="1"></label><p id="time"></p><svg id="sky" viewBox="0 0 420 365" role="img" aria-label="Небесная карта спутников"></svg><p id="sky-state"></p><label>Спутник / наблюдение RINEX <select id="satellite"></select></label><p>${esc(report.note||'')}</p>${report.truncated?'<p>Показан неполный набор: достигнут лимит объёма или файл прочитан не полностью.</p>':''}<p id="origin"></p><div id="plots"></div><script>(${runtime.toString()})(${json});<\/script></html>`;
}
const api={documentHTML};if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GNSSSatellites=api;
})(typeof window!=='undefined'?window:globalThis);