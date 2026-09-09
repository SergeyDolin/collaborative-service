'use strict';
const $=id=>document.getElementById(id);
const names={vertical:'Вертикально',horizontal:'Горизонтально',north:'С',east:'В',south:'Ю',west:'З'};
const state={task:null,busy:false,polling:false};
const protocols={
 full:'8 калибровочных сеансов: вертикально и горизонтально, С/В/Ю/З; плюс отдельный контрольный сеанс.',
 horizontal_only:'4 вертикальных сеанса С/В/Ю/З; плюс отдельный контрольный сеанс. Без координат марки определяются только «влево» и «вглубь».',
 quick:'1 вертикальный сеанс камерой на север и отдельный контроль в той же установке. Результат — поправка этой установки.'
};
function message(text,error=false){$('message').hidden=!text;$('message').textContent=text;$('message').classList.toggle('error',error);}
const number=id=>Number($(id).value);
const esc=v=>String(v??'—').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const mm=v=>typeof v==='number'&&Number.isFinite(v)?(v*1000).toFixed(2):'не определено';
async function api(path,options={}){
 const token=localStorage.getItem('token');if(!token){location.href='/login';throw Error('Войдите в сервис');}
 const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),120000);
 try{
  const r=await fetch('/api/calibration'+path,{...options,cache:'no-store',signal:controller.signal,headers:{Authorization:'Bearer '+token,...options.headers}});
  const data=await r.json().catch(()=>({}));
  if(!r.ok)throw Error(data.error||data.message||'Ошибка запроса '+r.status);
  return data;
 }finally{clearTimeout(timeout);}
}
function geometry(){return {reduceE:number('reduce-e'),reduceN:number('reduce-n'),reduceH:number('reduce-h'),control:$('purpose').value==='control'};}
function modeChanged(){
 $('protocol').textContent=protocols[$('mode').value];
 const none=$('reference').querySelector('option[value="none"]');none.disabled=$('mode').value!=='horizontal_only';
 if(none.disabled&&$('reference').value==='none')$('reference').value='geodetic';
 const known=$('reference').value==='geodetic';$('mark-fields').hidden=!known;
 for(const id of ['mark-lat','mark-lon','mark-h'])$(id).required=known;
}
function complete(t){
 const seen=new Set(t.sessions.filter(s=>!s.geometry.control).map(s=>s.position+'/'+s.orientation));
 const orientations=t.mode==='quick'?['north']:['north','east','south','west'];
 const positions=t.mode==='full'?['vertical','horizontal']:['vertical'];
 return positions.every(p=>orientations.every(o=>seen.has(p+'/'+o)))&&t.sessions.some(s=>s.geometry.control);
}
function showTask(t){
 state.task=t;restoreSetup(t);$('sessions-section').hidden=!t.hasReceiver && t.status==='pending';
 $('required').textContent=protocols[t.mode];
 $('expiry').textContent='Доступно до '+new Date(t.expiresAt).toLocaleString('ru-RU');
 $('sessions').innerHTML=(t.sessions||[]).map(s=>'<tr><td>'+esc(s.filename)+'</td><td>'+names[s.position]+' / '+names[s.orientation]+'</td><td>'+(s.geometry?.control?'Контроль':'Калибровка')+'</td><td>'+esc({pending:'Ожидает',processing:'Обработка',completed:'Готово',failed:'Ошибка'}[s.status]||s.status)+'</td></tr>').join('');
 const pending=t.status==='pending';$('session-fields').disabled=!pending;
 $('submit').disabled=!pending||!complete({...t,sessions:t.sessions||[]});
 $('position').value=t.mode==='full'?$('position').value:'vertical';
 $('position').disabled=t.mode!=='full';$('orientation').disabled=t.mode==='quick';if(t.mode==='quick')$('orientation').value='north';
 if(t.status==='failed')message(t.errorMessage||'Расчёт не выполнен',true);
 else if(t.status==='processing')message('Относительная обработка сеансов…');
 else if(t.status==='completed'&&t.result){message('Расчёт завершён');showResult(t);}
}
function restoreSetup(t){
 const values={'device-model':t.deviceModel,mode:t.mode,reference:t.refType,'mark-lat':t.refLat??0,'mark-lon':t.refLon??0,'mark-h':t.refH??0,'base-lat':t.options.baseLat,'base-lon':t.options.baseLon,'base-h':t.options.baseH,'reference-frame':t.options.referenceFrame,frequency:t.options.frequency};
 for(const [id,value] of Object.entries(values))$(id).value=value;
 modeChanged();
 $('setup-fields').disabled=false;
 for(const el of $('setup-fields').querySelectorAll('input,select,button'))el.disabled=true;
 if(t.status==='pending'&&!t.hasReceiver){$('base-file').disabled=false;$('create').disabled=false;$('create').textContent='Загрузить базу';}
}
function showResult(t){
 const r=t.result;$('result-section').hidden=false;
 $('result-kind').textContent=r.scope==='single-setup-offset'?'Поправка одной установки':'Среднее смещение в осях корпуса';
 $('offsets').innerHTML=[['Влево',r.offsetLeft,r.sigmaLeft],['Вглубь',r.offsetDepth,r.sigmaDepth],['Вниз от ARP',r.offsetDown,r.sigmaDown]].map(([name,value,sigma])=>'<div><p>'+name+'</p><p class="axis">'+mm(value)+(value===null?'':' мм')+'</p><p>Стандартная ошибка: '+mm(sigma)+(sigma===null?'':' мм')+'</p></div>').join('');
 const v=r.validation;
 $('validation').innerHTML=v?'<h3>Контроль: '+v.sessions+' сеанс(а)</h3><p>'+(t.refType==='none'?'Отклонения от оценённого положения оси установки':'Отклонения от известных координат марки')+'</p><table><thead><tr><th>Ось</th><th>RMS до, мм</th><th>RMS после, мм</th></tr></thead><tbody>'+['E','N','U'].map((axis,i)=>'<tr><td>'+axis+'</td><td>'+mm(v.before[i])+'</td><td>'+mm(v.after[i])+'</td></tr>').join('')+'</tbody></table>':'';
}
$('mode').onchange=modeChanged;$('reference').onchange=modeChanged;modeChanged();
$('setup-form').onsubmit=async event=>{
 event.preventDefault();if(state.busy)return;state.busy=true;$('create').disabled=true;
 try{
  if(!state.task){
   const payload={deviceModel:$('device-model').value,mode:$('mode').value,refType:$('reference').value,refLat:number('mark-lat'),refLon:number('mark-lon'),refH:number('mark-h'),options:{baseLat:number('base-lat'),baseLon:number('base-lon'),baseH:number('base-h'),referenceFrame:$('reference-frame').value,frequency:$('frequency').value}};
   const created=await api('/start',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
   state.task={...payload,id:created.taskId,status:'pending',hasReceiver:false};restoreSetup(state.task);history.replaceState(null,'','/calibration?task='+encodeURIComponent(created.taskId));
   state.task=await api('/'+created.taskId+'/status');restoreSetup(state.task);
  }
  const fd=new FormData();fd.append('file',$('base-file').files[0]);await api('/'+state.task.id+'/receiver',{method:'POST',body:fd});
  showTask(await api('/'+state.task.id+'/status'));message('База загружена. Добавьте сеансы смартфона.');
 }catch(e){message(e.message,true);}finally{state.busy=false;if(state.task?.options)restoreSetup(state.task);else $('create').disabled=false;}
};
$('session-form').onsubmit=async event=>{
 event.preventDefault();if(state.busy)return;state.busy=true;$('upload').disabled=true;
 try{
  const fd=new FormData();fd.append('file',$('session-file').files[0]);fd.append('position',$('position').value);fd.append('orientation',$('orientation').value);fd.append('geometry',JSON.stringify(geometry()));
  await api('/'+state.task.id+'/session',{method:'POST',body:fd});$('session-file').value='';
  showTask(await api('/'+state.task.id+'/status'));message('Сеанс добавлен');
 }catch(e){message(e.message,true);}finally{state.busy=false;$('upload').disabled=false;}
};
$('submit').onclick=async()=>{
 if(state.busy)return;state.busy=true;$('submit').disabled=true;
 try{await api('/'+state.task.id+'/submit',{method:'POST'});showTask(await api('/'+state.task.id+'/status'));}
 catch(e){message(e.message,true);$('submit').disabled=false;}finally{state.busy=false;}
};
async function poll(){
 if(!state.task||state.task.status!=='processing'||state.polling||document.hidden)return;
 state.polling=true;try{showTask(await api('/'+state.task.id+'/status'));}catch(e){message(e.message,true);}finally{state.polling=false;}
}
setInterval(poll,5000);
$('download').onclick=()=>{
 const t=state.task;if(!t?.result)return;
 const data={version:1,deviceModel:t.deviceModel,mode:t.mode,options:t.options,reference:{type:t.refType,B:t.refLat??null,L:t.refLon??null,H:t.refH??null},bodyAxes:['left','depth','down'],units:'m',timeSystem:'GPST',sessions:t.sessions.map(s=>({position:s.position,orientation:s.orientation,geometry:s.geometry})),result:t.result};
 const url=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));
 const a=document.createElement('a');a.href=url;a.download='antenna-calibration.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
};
(async()=>{const id=new URLSearchParams(location.search).get('task');if(id)try{
 const t=await api('/'+encodeURIComponent(id)+'/status');
 showTask(t);if(t.status==='pending'&&!t.hasReceiver)message('Загрузите файл базы для созданной задачи.');
}catch(e){message(e.message,true);}})();
