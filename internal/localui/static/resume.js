// The import is a preview. Only the explicit confirmation writes the profile.
const resumeDialog = $("#resume-dialog");
const resumeFile = $("#resume-file");
const resumeDropzone = $("#resume-dropzone");
const uploadResumeButton = $("#upload-resume");
const confirmResumeButton = $("#confirm-resume");
const resumeMessage = $("#resume-upload-message");
let resumeController;
let resumePreview;
let resumeFieldID = 0;

function setResumeFile(file) {
  if (!file) return;
  const supported = /\.(pdf|docx|json)$/i.test(file.name);
  const valid = supported && file.size > 0 && file.size <= 10 * 1024 * 1024;
  $("#resume-file-name").textContent = `${file.name} · ${(file.size / 1024).toFixed(0)} КБ`;
  uploadResumeButton.disabled = !valid;
  if (!valid) {
    resumeFile.value = "";
    setMessage(resumeMessage, supported ? "Выберите непустой файл до 10 МБ." : "Выберите PDF, DOCX или JSON. Старый DOC сохраните как DOCX.", true);
    return;
  }
  const transfer = new DataTransfer();
  transfer.items.add(file);
  resumeFile.files = transfer.files;
  setMessage(resumeMessage, "");
}

function resetResumeUpload() {
  resumePreview = undefined;
  resumeFile.value = "";
  resumeFile.required = true;
  $("#resume-file-name").textContent = "Файл не выбран";
  $("#resume-upload-step").hidden = false;
  $("#resume-preview").hidden = true;
  $("#resume-back").hidden = true;
  confirmResumeButton.hidden = true;
  uploadResumeButton.hidden = false;
  uploadResumeButton.disabled = true;
  resumeDialog.classList.remove("is-preview");
  $("#resume-title").textContent = "Прикрепить резюме";
 resumeDialog.querySelector(".resume-description").textContent = "Выберите файл. Перед сохранением можно проверить и исправить все поля.";
  setMessage(resumeMessage, "");
}

function closeResumeDialog() { resumeDialog.close(); }
resumeDialog.addEventListener("close", () => {
  if (resumeController) resumeController.abort();
  // Release potentially sensitive extracted text when leaving the preview.
  resumePreview = undefined;
  $("#resume-fields").replaceChildren();
  $("#resume-source").textContent = "";
  $("#resume-evidence").replaceChildren();
});
$("#open-resume").addEventListener("click", () => { resetResumeUpload(); resumeDialog.showModal(); });
$("#close-resume").addEventListener("click", closeResumeDialog);
$("#cancel-resume").addEventListener("click", closeResumeDialog);
$("#resume-back").addEventListener("click", resetResumeUpload);
resumeFile.addEventListener("change", () => setResumeFile(resumeFile.files[0]));
resumeDropzone.addEventListener("dragover", (event) => { event.preventDefault(); resumeDropzone.classList.add("is-dragging"); });
resumeDropzone.addEventListener("dragleave", () => resumeDropzone.classList.remove("is-dragging"));
resumeDropzone.addEventListener("drop", (event) => { event.preventDefault(); resumeDropzone.classList.remove("is-dragging"); setResumeFile(event.dataTransfer.files[0]); });
resumeDialog.addEventListener("click", (event) => { if (event.target === resumeDialog) closeResumeDialog(); });

function resumeField(parent, key, labelText, value, multiline = false, isList = false) {
  const label = document.createElement("label");
  const id = `resume-edit-${++resumeFieldID}`;
  label.className = "field-label";
  label.htmlFor = id;
  label.textContent = labelText;
  const field = document.createElement(multiline ? "textarea" : "input");
  field.id = id;
  field.dataset.field = key;
  if (isList) field.dataset.list = "true";
  field.value = isList ? (value || []).join("\n") : (value || "");
  field.maxLength = key === "name" || key === "title" ? 200 : 12000;
  if (multiline) field.rows = 3;
  parent.append(label, field);
  return field;
}

const resumeSections = {
  experience: { label: "Опыт работы", item: "Место работы", fields: [["position", "Должность"], ["company", "Компания"], ["period", "Период"], ["responsibilities", "Задачи и достижения — по одному в строке", true, true]] },
  projects: { label: "Учебные и личные проекты", item: "Проект", fields: [["name", "Название"], ["description", "Описание и статус проекта", true], ["technologies", "Технологии — по одной в строке", true, true], ["link", "Ссылка"]] },
  achievements: { label: "Личные достижения", item: "Достижение", fields: [["name", "Конкурс, олимпиада или награда"], ["result", "Результат — участие, финал, место или награда", true], ["date", "Дата или год"], ["organizer", "Организатор"], ["link", "Ссылка"]] },
  education: { label: "Образование", item: "Образование", fields: [["degree", "Программа или степень"], ["university", "Учебное заведение"], ["year", "Год или период"]] },
  languages: { label: "Языки", item: "Язык", fields: [["language", "Язык"], ["level", "Уровень"]] },
};

function resumeCollection(parent, key, items) {
  const spec = resumeSections[key];
  const section = document.createElement("details");
  section.open = Boolean(items && items.length);
  section.dataset.section = key;
  const heading = document.createElement("summary");
  const rows = document.createElement("div");
  rows.className = "resume-records";
  const updateCount = () => { heading.textContent = `${spec.label} (${rows.children.length})`; };
  function addRow(item = {}) {
    const row = document.createElement("fieldset");
    row.className = "resume-record";
    const legend = document.createElement("legend");
    legend.textContent = spec.item;
    row.append(legend);
    spec.fields.forEach(([field, label, multiline, list]) => resumeField(row, field, label, item[field], multiline, list));
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "text-button resume-remove";
    remove.textContent = "Удалить запись";
    remove.addEventListener("click", () => { row.remove(); updateCount(); });
    row.append(remove);
    rows.append(row);
    updateCount();
  }
  const add = document.createElement("button");
  add.type = "button";
  add.className = "text-button resume-add";
  add.textContent = `+ Добавить: ${spec.item.toLowerCase()}`;
  add.addEventListener("click", () => addRow());
  section.append(heading, rows, add);
  (items || []).forEach(addRow);
  updateCount();
  parent.append(section);
}

function showResumePreview(preview, editing = false) {
  resumePreview = preview;
  const profile = preview.profile;
  const fields = $("#resume-fields");
  fields.replaceChildren();
  const basic = document.createElement("div");
  basic.dataset.section = "basic";
  resumeField(basic, "name", "Имя", profile.name);
  resumeField(basic, "title", "Желаемая должность", profile.title);
  resumeField(basic, "summary", "О себе — только сведения из резюме", profile.summary, true).maxLength = 5000;
  resumeField(basic, "skills", "Навыки — по одному в строке", profile.skills, true, true);
  fields.append(basic);
  Object.keys(resumeSections).forEach((key) => resumeCollection(fields, key, profile[key]));
  const contact = document.createElement("details");
  contact.dataset.section = "contact";
  const contactTitle = document.createElement("summary");
  contactTitle.textContent = "Контакты";
  contact.append(contactTitle);
  [["email", "Email"], ["phone", "Телефон"], ["linkedin", "LinkedIn"], ["github", "GitHub"]].forEach(([key, label]) => resumeField(contact, key, label, (profile.contact || {})[key]));
  fields.append(contact);
  const warnings = $("#resume-warnings");
  warnings.replaceChildren();
  (preview.warnings || []).forEach((text) => { const item = document.createElement("p"); item.textContent = text; warnings.append(item); });
  const evidence = $("#resume-evidence");
  evidence.replaceChildren();
  const labels = {name:"Имя",title:"Должность",summary:"О себе",skills:"Навыки",experience:"Опыт",projects:"Проекты",achievements:"Личные достижения",education:"Образование",languages:"Языки",contact:"Контакты"};
  (preview.evidence || []).forEach((item) => {
    const quote = document.createElement("blockquote");
    const label = document.createElement("strong");
    label.textContent = `${labels[item.section] || "Цитата"}: `;
    quote.append(label, document.createTextNode(item.quote));
    evidence.append(quote);
  });
  $("#resume-evidence-section").hidden = !(preview.evidence || []).length;
  $("#resume-source").textContent = preview.source_text || "";
  $("#resume-source-section").hidden = !preview.source_text;
  $("#resume-upload-step").hidden = true;
  $("#resume-preview").hidden = false;
  $("#resume-back").hidden = editing;
  uploadResumeButton.hidden = true;
  confirmResumeButton.hidden = false;
  confirmResumeButton.disabled = false;
  resumeFile.required = false;
  resumeDialog.classList.add("is-preview");
  $("#resume-title").textContent = editing ? "Редактировать профиль" : "Проверьте резюме";
 resumeDialog.querySelector(".resume-description").textContent = "Исправьте нужные поля и подтвердите сохранение.";
  setMessage(resumeMessage, "");
  resumeDialog.scrollTop = 0;
}

function readResumeFields(parent) {
  const result = {};
  parent.querySelectorAll("[data-field]").forEach((field) => {
    result[field.dataset.field] = field.dataset.list ? field.value.split("\n").map((s) => s.trim()).filter(Boolean) : field.value.trim();
  });
  return result;
}

function readResumePreview() {
  const fields = $("#resume-fields");
  const profile = readResumeFields(fields.querySelector("[data-section='basic']"));
  Object.keys(resumeSections).forEach((key) => {
    profile[key] = [...fields.querySelector(`[data-section='${key}']`).querySelectorAll("fieldset")].map(readResumeFields);
  });
  profile.contact = readResumeFields(fields.querySelector("[data-section='contact']"));
  return profile;
}

$("#resume-upload-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (resumePreview) { confirmResumeButton.click(); return; }
  if (!resumeFile.files[0]) return;
  resumeController = new AbortController();
  const controller = resumeController;
  uploadResumeButton.disabled = true;
  uploadResumeButton.textContent = "Разбираю…";
  const isJSON = /\.json$/i.test(resumeFile.files[0].name);
  setMessage(resumeMessage, isJSON ? "Проверяю профиль…" : "Извлекаю текст и разбираю резюме с помощью ИИ. Это может занять до 3 минут…");
  try {
    const formData = new FormData();
    formData.append("resume", resumeFile.files[0]);
    const preview = await request("/api/resume", {method:"POST",body:formData,signal:controller.signal});
    if (resumeDialog.open && resumeController === controller) showResumePreview(preview);
  } catch (error) {
    if (error.name !== "AbortError" && resumeController === controller) { setMessage(resumeMessage, error.message, true); uploadResumeButton.disabled = false; }
  } finally {
    if (resumeController === controller) { resumeController = undefined; uploadResumeButton.textContent = "Разобрать резюме"; }
  }
});

confirmResumeButton.addEventListener("click", async () => {
  if (!resumePreview) return;
  if (!$("#resume-upload-form").reportValidity()) return;
  confirmResumeButton.disabled = true;
  setMessage(resumeMessage, "Сохраняю проверенный профиль…");
  try {
    const result = await request("/api/resume/confirm", {method:"POST",body:JSON.stringify(readResumePreview())});
    fillProfile(await request("/api/profile"));
    setMessage($("#profile-message"), result.status);
    resumeDialog.close();
  } catch (error) { setMessage(resumeMessage, error.message, true); confirmResumeButton.disabled = false; }
});

$("#edit-profile-details").addEventListener("click", async () => {
  try {
    const profile = await request("/api/profile/details");
    resetResumeUpload();
    showResumePreview({profile}, true);
    resumeDialog.showModal();
  } catch (error) { setMessage($("#profile-message"), error.message, true); }
});
