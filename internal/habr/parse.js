() => {
 const text = el => (el?.textContent || '').replace(/\u00a0/g,' ').replace(/[ \t]+/g,' ').trim();
 const cleanURL = raw => { try { const u = new URL(raw, 'https://career.habr.com'); return u.origin === 'https://career.habr.com' && /^\/vacancies\/\d+$/.test(u.pathname) ? u.origin + u.pathname : ''; } catch { return ''; } };
 const salary = el => { if (!el || el.querySelector('.predicted-salary')) return ''; return text(el); };
 const cards = [...document.querySelectorAll('.vacancy-card')].map(c => ({
  url: cleanURL(c.querySelector('.vacancy-card__title-link')?.getAttribute('href')),
  title: text(c.querySelector('.vacancy-card__title-link')),
  company: text(c.querySelector('.vacancy-card__company')),
  salary: salary(c.querySelector('.vacancy-card__salary')),
 })).filter(j => j.url && j.title);
 const header = document.querySelector('.vacancy-header');
 let detail = null;
 if (header) {
  const desc = document.querySelector('.vacancy-description__text')?.cloneNode(true);
  desc?.querySelectorAll('script,style,form,button,input,textarea').forEach(e => e.remove());
  desc?.querySelectorAll('p,li,div,br,h2,h3,h4').forEach(e => e.append('\n'));
  const groups = [...header.querySelectorAll('.content-section')];
  const group = name => groups.find(g => text(g.querySelector('h2')) === name);
  const chips = g => [...(g?.querySelectorAll('.basic-chip') || [])].map(text).filter(Boolean);
  const requirements = chips(group('Требования'));
  const conditions = chips(group('Условия'));
  detail = {
   title: text(header.querySelector('h1')), company: text(header.querySelector('.vacancy-company__title .link-comp')),
   salary: salary(header.querySelector('.vacancy-header__salary')),
   description: text(desc), requirements, conditions,
   locations: [...header.querySelectorAll('a[href*="city_id="]')].map(text).filter(Boolean),
   grade: requirements.find(x => /^(Intern|Junior|Middle|Senior|Lead)$/i.test(x)) || '',
   published: header.querySelector('time')?.getAttribute('datetime') || text(header.querySelector('.vacancy-header__date')),
  };
 }
 const next = document.querySelector('a[rel="next"]')?.getAttribute('href') || '';
 return {cards, detail, next};
}
