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
  const response = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `Ошибка ${response.status}`);
  return data;
}

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
