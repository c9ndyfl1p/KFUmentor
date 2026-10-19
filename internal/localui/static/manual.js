// Manual actions use the same serialized browser queue as automatic search.
const manualDialog = document.querySelector('#application-dialog');
const manualLetters = new Map();
let manualOpenID = null;
let manualPreviewVersion = '';
const policySwitch = document.querySelector('#application-policy');
try { policySwitch.checked = localStorage.getItem('matchmentor.applicationPolicy') !== 'score'; } catch { policySwitch.checked = true; }
function updatePolicyLabel() {
 document.querySelector('#application-policy-description').textContent = policySwitch.checked
  ? 'По рекомендации: apply отправляется независимо от балла; review от порога остаётся для вашего решения.'
  : 'По оценке: все вакансии от порога допускаются к отклику, включая review и skip. Письмо проходит проверку фактов.';
}
policySwitch.addEventListener('change', () => { try { localStorage.setItem('matchmentor.applicationPolicy', policySwitch.checked ? 'strict' : 'score'); } catch {} updatePolicyLabel(); });
updatePolicyLabel();
async function vacancyAction(id, action, version = '') {
 return request('/api/vacancy/action', { method:'POST', body:JSON.stringify({id, action, version}) });
}
function updateManualPreview() {
 if (!manualOpenID) return;
 const card = manualLetters.get(manualOpenID);
 if (!card) return;
 document.querySelector('#application-title').textContent = card.title || 'Отклик на вакансию';
 const preparing = card.status === 'queued' && ['Письмо в очереди','Готовлю письмо для просмотра'].includes(card.stage);
 const failed = card.status === 'error';
 document.querySelector('#application-stage').textContent = preparing ? card.stage : (card.details || card.stage || '');
 document.querySelector('#application-letter').textContent = preparing
  ? 'Готовится новая версия письма. Дождитесь завершения подготовки: затем появится текст и станет доступна кнопка «Отправить».'
  : failed ? 'Этот черновик недоступен для отправки. Закройте окно и нажмите «Откликнуться» в карточке, чтобы подготовить письмо заново.'
  : card.letter || 'Письмо готовится в очереди между вакансиями. Автоматической отправки не будет.';
 manualPreviewVersion = card.version || '';
 document.querySelector('#application-send').disabled = !card.canSend || !card.letter;
}
function renderManualActions(card, container) {
 if (!card.id) return;
 manualLetters.set(card.id,card);
 if (card.reviewPoints?.length) {
  const notes=document.createElement('ul');notes.className='review-points';
  for(const point of card.reviewPoints.slice(0,3)){const li=document.createElement('li');li.textContent=point;notes.append(li);}
  container.append(notes);
 }
 const actions=document.createElement('div');actions.className='vacancy-actions';
 function button(label, fn) {
  const b=document.createElement('button');b.type='button';b.className='button button-secondary';b.textContent=label;
  b.addEventListener('click',async()=>{b.disabled=true;try{await fn();await refreshState();}catch(error){showActivityMessage(error.message,true);b.disabled=false;}});actions.append(b);
 }
 if(card.canDraft||card.canSend) {
  button(card.canSend?'Просмотреть письмо':'Откликнуться',async()=>{
   if(!card.canSend) await vacancyAction(card.id,'draft');
   manualOpenID=card.id;updateManualPreview();if(!manualDialog.open)manualDialog.showModal();
  });
  button('Пропустить',()=>vacancyAction(card.id,'dismiss'));
 }
 if(card.canRetry)button('Повторить оценку',()=>vacancyAction(card.id,'retry'));
 container.append(actions);updateManualPreview();
}
window.renderManualActions=renderManualActions;
document.querySelector('#application-close').addEventListener('click',()=>{manualDialog.close();manualOpenID=null;manualPreviewVersion='';});
manualDialog.addEventListener('cancel',()=>{manualOpenID=null;manualPreviewVersion='';});
document.querySelector('#application-send').addEventListener('click',async(event)=>{
 const button=event.currentTarget;button.disabled=true;
 try {await vacancyAction(manualOpenID,'send',manualPreviewVersion);await refreshState();}
 catch(error){document.querySelector('#application-stage').textContent=error.message;}
});
