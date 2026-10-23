(() => {
 const el = id => document.getElementById(id);
 let initialized = false, signature = '';
 const node = (tag, text) => {const n = document.createElement(tag);n.textContent=text;return n;};
 async function request(method='GET',body) {
  const r=await fetch('/api/habr',{method,headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});
  const data=await r.json();if(!r.ok)throw new Error(data.error||'Не удалось выполнить сбор');return data;
 }
 function render(s) {
  if(!initialized){if(s.url)el('habr-url').value=s.url;if(s.limit)el('habr-limit').value=s.limit;initialized=true;}
  el('habr-start').disabled=!!s.running;el('habr-url').disabled=!!s.running;el('habr-limit').disabled=!!s.running;
  el('habr-stop').hidden=!s.running;el('habr-status').textContent=s.status||'Готов к сбору';
  const key=JSON.stringify(s.jobs);if(key===signature)return;signature=key;
  el('habr-cards').replaceChildren();
  for(const j of s.jobs||[]) {
   const card=node('article','');card.className='card';card.style.cssText='padding:20px;margin-top:16px';
   card.append(node('small','Хабр Карьера · Без оценки'),node('h3',j.title||'Вакансия'),node('p',j.company||'Компания не указана'),node('strong',j.salary||'Зарплата не указана'));
   card.append(node('p',(j.conditions||[]).join(' · ')),node('p',(j.requirements||[]).join(' · ')));
   if(j.published){const date=new Date(j.published);card.append(node('p','Дата на сайте: '+(Number.isNaN(date.getTime())?j.published:date.toLocaleDateString('ru-RU'))));}
   if(j.error)card.append(node('p',j.error));
   if(j.description){const d=node('details','');d.append(node('summary','Описание вакансии'));const p=node('p',j.description);p.style.whiteSpace='pre-wrap';d.append(p);card.append(d);}
   try {const u=new URL(j.url);if(u.origin==='https://career.habr.com'&&/^\/vacancies\/\d+$/.test(u.pathname)){const a=node('a','Открыть на Хабр Карьере');a.href=u.href;a.target='_blank';a.rel='noopener noreferrer';card.append(a);}}catch{}
   el('habr-cards').append(card);
  }
 }
 el('habr-form').addEventListener('submit',async e=>{e.preventDefault();el('habr-start').disabled=true;try{render(await request('POST',{url:el('habr-url').value,limit:Number(el('habr-limit').value)}));}catch(err){el('habr-status').textContent=err.message;el('habr-start').disabled=false;}});
 el('habr-stop').addEventListener('click',async()=>{try{render(await request('DELETE'));}catch(err){el('habr-status').textContent=err.message;}});
 async function poll(){try{render(await request());}catch(err){el('habr-status').textContent=err.message;}finally{setTimeout(poll,2500);}}
 poll();
})();
