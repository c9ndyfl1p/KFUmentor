(() => {
  const el = id => document.getElementById(id);
  const node = (tag, text) => { const n = document.createElement(tag); n.textContent = text; return n; };
  let signature = '';
  async function request(method = 'GET', body) {
    const r = await fetch('/api/habr', { method, headers: {'Content-Type':'application/json'}, body: body ? JSON.stringify(body) : undefined });
    const data = await r.json();
    if (!r.ok) throw new Error(data.error || 'Не удалось собрать вакансии Хабра');
    return data;
  }
  function render(s) {
    window.habrRunning = Boolean(s.running);
    window.habrStatus = s.status;
    el('habr-status').textContent = s.status || 'Здесь появятся вакансии Хабр Карьеры после поиска.';
    const key = JSON.stringify([s.jobs, s.running, s.minimumScore, el('habr-show-all').checked]);
    if (key === signature) return;
    signature = key;
    const list = el('habr-cards');
    list.replaceChildren();
    for (const [index, j] of (s.jobs || []).entries()) {
      if (!el('habr-show-all').checked && j.status === 'assessed' && j.score && j.score < (s.minimumScore || 0)) continue;
      const card = node('article', '');
      card.className = 'vacancy-progress-card';
      card.dataset.status = j.error ? 'error' : j.status === 'running' ? 'running' : j.recommendation === 'review' ? 'review' : 'recommended';
      const heading = node('div', ''); heading.className = 'vacancy-progress-heading';
      const position = node('span', `Вакансия ${index + 1}`); position.className = 'vacancy-position';
      const state = node('span', j.error ? 'Ошибка' : j.status === 'running' ? 'Оценивается' : j.score ? ({apply:'Рекомендована',review:'Нужна проверка',skip:'Не рекомендована'}[j.recommendation] || 'Оценена') : 'Без оценки'); state.className = 'vacancy-state';
      heading.append(position, state);
      const title = node('h3', ''); title.className = 'vacancy-progress-title';
      try {
        const url = new URL(j.url);
        if (url.origin === 'https://career.habr.com' && /^\/vacancies\/\d+$/.test(url.pathname)) {
          const a = node('a', j.title || 'Вакансия'); a.href = url.href; a.target = '_blank'; a.rel = 'noopener noreferrer'; title.append(a);
        }
      } catch {}
      if (!title.textContent) title.textContent = j.title || 'Вакансия';
      const salary = node('p', j.salary || 'Зарплата не указана'); salary.className = 'vacancy-salary';
      const company = node('p', j.company || 'Компания не указана'); company.className = 'vacancy-progress-stage';
      card.append(heading, title, salary, company);
      if (j.score) { const score = node('span', `Совпадение ${j.score}/10`); score.className = 'vacancy-score'; card.append(score); }
      if (j.reason) card.append(node('p', j.reason));
      if (j.reviewPoints?.length) { const ul = node('ul', ''); for (const point of j.reviewPoints.slice(0,3)) ul.append(node('li', point)); card.append(ul); }
      if (j.analysis) { const d = node('details', ''); d.append(node('summary','Разбор соответствия'), node('p',j.analysis)); card.append(d); }
      if (j.locations?.length) card.append(node('p', `Город: ${j.locations.join(', ')}`));
      if (j.grade) card.append(node('p', `Уровень: ${j.grade}`));
      if (j.requirements?.length) card.append(node('p', `Требования: ${j.requirements.join(' · ')}`));
      if (j.conditions?.length) card.append(node('p', `Условия: ${j.conditions.join(' · ')}`));
      if (j.error) card.append(node('p', j.error));
      if (j.error || j.status === 'stopped' || !j.score) { const retry = node('button', 'Повторить оценку'); retry.type='button'; retry.className='button button-secondary'; retry.disabled=!!s.running; retry.addEventListener('click', async()=>{ retry.disabled=true; try { render(await request('POST',{retryURL:j.url})); } catch(err) { el('habr-status').textContent=err.message; retry.disabled=false; } }); card.append(retry); }
      if (j.description) {
        const detail = node('details', ''); detail.append(node('summary', 'Описание вакансии'));
        const p = node('p', j.description); p.style.whiteSpace = 'pre-wrap'; detail.append(p); card.append(detail);
      }
      list.append(card);
    }
  }
  window.startHabrSearch = async (task, limit) => render(await request('POST', {task, limit, minimumScore: Number(el('minimum-score').value), areaName: el('area-id').value ? (el('city').dataset.areaName || el('city').value.split(' · ')[0]).trim() : ''}));
  window.stopHabrSearch = async () => render(await request('DELETE'));
  async function poll() { try { render(await request()); } catch (err) { el('habr-status').textContent = err.message; } finally { setTimeout(poll, 2500); } }
  el('habr-show-all').addEventListener('change', ()=>{ signature=''; request().then(render).catch(err=>{el('habr-status').textContent=err.message;}); });
  poll();
})();
