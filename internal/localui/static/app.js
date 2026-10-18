const $ = (selector) => document.querySelector(selector);
const taskForm = $("#run-form");
const profileForm = $("#profile-form");
const runButton = $("#run-button");
const output = $("#output");
const statusChip = $("#status-chip");
const statusText = $("#status-text");
const applyNote = $("#apply-note");

let lastOutput = "";

async function request(path, options = {}) {
  const headers = options.body instanceof FormData
    ? { ...(options.headers || {}) }
    : { "Content-Type": "application/json", ...(options.headers || {}) };
  const response = await fetch(path, {
    ...options,
    headers,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `Ошибка ${response.status}`);
  return data;
}

const resumeDialog = $("#resume-dialog");
const resumeFile = $("#resume-file");
const resumeDropzone = $("#resume-dropzone");
const uploadResumeButton = $("#upload-resume");
const resumeFileName = $("#resume-file-name");
const resumeUploadMessage = $("#resume-upload-message");

function setResumeFile(file) {
  if (!file) return;
  if (!file.name.toLowerCase().endsWith(".json")) {
    resumeFile.value = "";
    uploadResumeButton.disabled = true;
    resumeFileName.textContent = file.name;
    setMessage(resumeUploadMessage, "Выберите файл JSON.", true);
    return;
  }
  const transfer = new DataTransfer();
  transfer.items.add(file);
  resumeFile.files = transfer.files;
  resumeFileName.textContent = `${file.name} · ${(file.size / 1024).toFixed(0)} КБ`;
  uploadResumeButton.disabled = false;
  setMessage(resumeUploadMessage, "");
}

function closeResumeDialog() {
  resumeDialog.close();
}

$("#open-resume").addEventListener("click", () => {
  resumeFile.value = "";
  resumeFileName.textContent = "Файл не выбран";
  uploadResumeButton.disabled = true;
  setMessage(resumeUploadMessage, "");
  resumeDialog.showModal();
});
$("#close-resume").addEventListener("click", closeResumeDialog);
$("#cancel-resume").addEventListener("click", closeResumeDialog);
resumeFile.addEventListener("change", () => setResumeFile(resumeFile.files[0]));
resumeDropzone.addEventListener("dragover", (event) => {
  event.preventDefault();
  resumeDropzone.classList.add("is-dragging");
});
resumeDropzone.addEventListener("dragleave", () => resumeDropzone.classList.remove("is-dragging"));
resumeDropzone.addEventListener("drop", (event) => {
  event.preventDefault();
  resumeDropzone.classList.remove("is-dragging");
  setResumeFile(event.dataTransfer.files[0]);
});
resumeDialog.addEventListener("click", (event) => {
  if (event.target === resumeDialog) closeResumeDialog();
});
$("#resume-upload-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!resumeFile.files[0]) return;
  uploadResumeButton.disabled = true;
  uploadResumeButton.textContent = "Загружаю…";
  setMessage(resumeUploadMessage, "Проверяю и сохраняю профиль…");
  try {
    const formData = new FormData();
    formData.append("resume", resumeFile.files[0]);
    const result = await request("/api/resume", { method: "POST", body: formData });
    fillProfile(await request("/api/profile"));
    setMessage($("#profile-message"), result.status);
    resumeDialog.close();
  } catch (error) {
    setMessage(resumeUploadMessage, error.message, true);
    uploadResumeButton.disabled = false;
  } finally {
    uploadResumeButton.textContent = "Загрузить резюме";
  }
});

function setMessage(element, message, isError = false) {
  element.textContent = message;
  element.classList.toggle("is-error", isError);
}

function fillProfile(profile) {
  $("#name").value = profile.name || "";
  $("#title").value = profile.title || "";
  $("#summary").value = profile.summary || "";
  $("#skills").value = (profile.skills || []).join(", ");
  $("#experience").value = profile.experience || "";
}

async function loadProfile() {
  try {
    fillProfile(await request("/api/profile"));
  } catch (error) {
    setMessage($("#profile-message"), error.message, true);
  }
}

profileForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#save-profile");
  button.disabled = true;
  setMessage($("#profile-message"), "Сохраняю…");
  try {
    const data = {
      name: $("#name").value,
      title: $("#title").value,
      summary: $("#summary").value,
      skills: $("#skills").value.split(",").map((skill) => skill.trim()).filter(Boolean),
      experience: $("#experience").value,
    };
    const result = await request("/api/profile", { method: "PUT", body: JSON.stringify(data) });
    setMessage($("#profile-message"), result.status);
  } catch (error) {
    setMessage($("#profile-message"), error.message, true);
  } finally {
    button.disabled = false;
  }
});

$("#auto-apply").addEventListener("change", (event) => {
  applyNote.hidden = !event.target.checked;
});

taskForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  runButton.disabled = true;
  const label = runButton.querySelector("span");
  label.textContent = "Запускаю…";
  try {
    await request("/api/run", {
      method: "POST",
      body: JSON.stringify({
        task: $("#task").value,
        autoApply: $("#auto-apply").checked,
        visible: $("#visible-browser").checked,
      }),
    });
    output.textContent = "Задача передана агенту. Жду результат…";
    lastOutput = output.textContent;
    await refreshState();
  } catch (error) {
    output.textContent = error.message;
    lastOutput = output.textContent;
  } finally {
    const state = await request("/api/state").catch(() => ({ running: false }));
    runButton.disabled = state.running;
    label.textContent = state.running ? "Идёт поиск…" : "Начать поиск";
  }
});

async function refreshState() {
  const state = await request("/api/state");
  statusText.textContent = state.status || "Готов к поиску";
  statusChip.classList.toggle("is-running", Boolean(state.running));
  statusChip.classList.toggle("is-done", !state.running && state.status === "Поиск завершён");
  statusChip.classList.toggle("is-error", !state.running && /ошибк|не удалось/i.test(state.status || ""));
  runButton.disabled = Boolean(state.running);
  runButton.querySelector("span").textContent = state.running ? "Идёт поиск…" : "Начать поиск";

  if (state.output && state.output !== lastOutput) {
    output.textContent = state.output;
    output.scrollTop = output.scrollHeight;
    lastOutput = state.output;
  } else if (!state.output && lastOutput && !state.running) {
    output.textContent = "Журнал очищен. Запустите новый поиск, чтобы увидеть результат.";
    lastOutput = "";
  }

  $("#activity-caption").textContent = state.startedAt
    ? `Последний запуск: ${new Date(state.startedAt).toLocaleString("ru-RU")}`
    : "Здесь появится статус и результат последнего запуска";
}

$("#clear-log").addEventListener("click", async () => {
  await request("/api/clear", { method: "POST" }).catch(() => {});
  output.textContent = "Журнал очищен. Запустите новый поиск, чтобы увидеть результат.";
  lastOutput = "";
});

loadProfile();
refreshState().catch((error) => {
  statusText.textContent = "Нет связи с локальным агентом";
  statusChip.classList.add("is-error");
  output.textContent = error.message;
});
window.setInterval(() => refreshState().catch(() => {}), 1200);
