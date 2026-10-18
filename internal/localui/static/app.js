const $ = (selector) => document.querySelector(selector);
const taskForm = $("#run-form");
const profileForm = $("#profile-form");
const runButton = $("#run-button");
const stopButton = $("#stop-button");
const loginButton = $("#login-button");
const vacancyCards = $("#vacancy-cards");
const activityMessage = $("#activity-message");
const statusChip = $("#status-chip");
const statusText = $("#status-text");
const applyNote = $("#apply-note");
const cityField = $("#city");
const areaIDField = $("#area-id");
const citySuggestions = $("#city-suggestions");
const cityHint = $("#city-hint");

let citySearchTimer;
let citySearchController;
let activeCityOption = -1;

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

function setMessage(element, message, isError = false) {
  element.textContent = message;
  element.classList.toggle("is-error", isError);
}

function showActivityMessage(message = "", isError = false) {
  activityMessage.textContent = message;
  activityMessage.hidden = !message;
  activityMessage.classList.toggle("is-error", isError);
  activityMessage.setAttribute("role", isError ? "alert" : "status");
}

function renderVacancyCards(cards, running) {
  vacancyCards.replaceChildren();
  if (!cards.length) {
    const empty = document.createElement("p");
    empty.className = "activity-empty";
    empty.textContent = running
      ? "Ищу вакансии. Карточки появятся, когда агент начнёт их оценивать."
      : "Здесь появятся карточки найденных вакансий.";
    vacancyCards.append(empty);
    return;
  }

  for (const item of cards) {
    const card = document.createElement("article");
    card.className = "vacancy-progress-card";
    card.dataset.status = item.status || "running";

    const heading = document.createElement("div");
    heading.className = "vacancy-progress-heading";
    const position = document.createElement("span");
    position.className = "vacancy-position";
    position.textContent = `Вакансия ${item.index}${item.total ? ` из ${item.total}` : ""}`;
    const state = document.createElement("span");
    state.className = "vacancy-state";
    state.textContent = ({ running: "В работе", applying: "Отклик", success: "Готово", skipped: "Пропущена", stopped: "Остановлено", error: "Ошибка" })[item.status] || "В работе";
    heading.append(position, state);

    const title = document.createElement("h3");
    title.className = "vacancy-progress-title";
    if (item.url) {
      const link = document.createElement("a");
      link.href = item.url;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.textContent = item.title || "Вакансия";
      link.setAttribute("aria-label", `Открыть вакансию: ${item.title || "Вакансия"}`);
      title.append(link);
    } else {
      title.textContent = item.title || "Вакансия";
    }

    const progress = document.createElement("p");
    progress.className = "vacancy-progress-stage";
    progress.textContent = item.stage || "Ожидает обработки";

    card.append(heading, title, progress);
    if (item.score) {
      const score = document.createElement("span");
      score.className = "vacancy-score";
      score.textContent = `Совпадение ${item.score}/10`;
      card.append(score);
    }
    if (item.result) {
      const result = document.createElement("p");
      result.className = "vacancy-result";
      result.textContent = item.result;
      card.append(result);
    }
    if (item.summary) {
      const summary = document.createElement("p");
      summary.className = "vacancy-result";
      summary.textContent = item.summary;
      card.append(summary);
    }
    if (item.details) {
      const details = document.createElement("p");
      details.className = "vacancy-details";
      details.textContent = item.details;
      details.setAttribute("role", "alert");
      card.append(details);
    }
    vacancyCards.append(card);
  }
}

function hideCitySuggestions() {
  citySuggestions.hidden = true;
  cityField.setAttribute("aria-expanded", "false");
  cityField.removeAttribute("aria-activedescendant");
  activeCityOption = -1;
}

function chooseCity(item) {
  areaIDField.value = String(item.id);
  cityField.value = item.path ? `${item.name} · ${item.path}` : item.name;
  cityHint.textContent = `Выбран регион hh.ru: ${cityField.value}`;
  cityHint.classList.remove("is-error");
  hideCitySuggestions();
}

function setActiveCityOption(index) {
  const options = [...citySuggestions.querySelectorAll("[role='option']")];
  if (!options.length) return;
  activeCityOption = (index + options.length) % options.length;
  options.forEach((option, optionIndex) => {
    const active = optionIndex === activeCityOption;
    option.classList.toggle("is-active", active);
    option.setAttribute("aria-selected", String(active));
  });
  const activeOption = options[activeCityOption];
  cityField.setAttribute("aria-activedescendant", activeOption.id);
  activeOption.scrollIntoView({ block: "nearest" });
}

function showCityOptions(items) {
  citySuggestions.replaceChildren();
  if (!items.length) {
    hideCitySuggestions();
    cityHint.textContent = "Подходящих городов не нашлось. Попробуйте другое написание.";
    return;
  }
  items.forEach((item, index) => {
    const option = document.createElement("button");
    option.type = "button";
    option.id = `city-option-${index}`;
    option.className = "city-suggestion";
    option.setAttribute("role", "option");
    option.setAttribute("aria-selected", "false");
    const name = document.createElement("strong");
    name.textContent = item.name;
    option.append(name);
    if (item.path) {
      const path = document.createElement("small");
      path.textContent = item.path;
      option.append(path);
    }
    option.addEventListener("mousedown", (event) => event.preventDefault());
    option.addEventListener("click", () => chooseCity(item));
    citySuggestions.append(option);
  });
  citySuggestions.hidden = false;
  cityField.setAttribute("aria-expanded", "true");
  cityHint.textContent = "Выберите один из регионов hh.ru.";
}

cityField.addEventListener("input", () => {
  areaIDField.value = "";
  cityHint.textContent = "Оставьте поле пустым, чтобы искать по всей России.";
  cityHint.classList.remove("is-error");
  hideCitySuggestions();
  if (citySearchController) citySearchController.abort();
  clearTimeout(citySearchTimer);

  const query = cityField.value.trim();
  if (query.length < 2) return;

  citySearchTimer = setTimeout(async () => {
    citySearchController = new AbortController();
    cityHint.textContent = "Ищу город или регион…";
    try {
      const data = await request(`/api/areas?q=${encodeURIComponent(query)}`, { signal: citySearchController.signal });
      showCityOptions(data.items || []);
    } catch (error) {
      if (error.name === "AbortError") return;
      cityHint.textContent = error.message;
      cityHint.classList.add("is-error");
    }
  }, 300);
});

cityField.addEventListener("keydown", (event) => {
  const options = citySuggestions.querySelectorAll("[role='option']");
  if (event.key === "ArrowDown" && options.length) {
    event.preventDefault();
    setActiveCityOption(activeCityOption + 1);
  } else if (event.key === "ArrowUp" && options.length) {
    event.preventDefault();
    setActiveCityOption(activeCityOption < 0 ? options.length - 1 : activeCityOption - 1);
  } else if (event.key === "Enter" && activeCityOption >= 0 && options[activeCityOption]) {
    event.preventDefault();
    options[activeCityOption].click();
  } else if (event.key === "Escape") {
    hideCitySuggestions();
  }
});

cityField.addEventListener("blur", () => {
  setTimeout(hideCitySuggestions, 120);
});

function fillProfile(profile) {
  $("#name").value = profile.name || "";
  $("#title").value = profile.title || "";
  $("#summary").value = profile.summary || "";
  $("#skills").value = (profile.skills || []).join(", ");
  $("#experience").value = profile.experience || "";
 $("#projects").value = profile.projects || "";
 $("#achievements").value = profile.achievements || "";
  if (!$("#hr-position").value) $("#hr-position").value = profile.title || "";
}

const hrReplyForm = $("#hr-reply-form");
const hrReplyField = $("#hr-reply");
const copyReplyButton = $("#copy-reply");
hrReplyForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#draft-reply");
  button.disabled = true;
  button.textContent = "Готовлю ответ…";
  setMessage($("#hr-message-status"), "Отправляю контекст модели…");
  try {
    const result = await request("/api/hr/reply", {
      method: "POST",
      body: JSON.stringify({
        company: $("#hr-company").value,
        position: $("#hr-position").value,
        context: $("#hr-context").value,
        message: $("#hr-message").value,
      }),
    });
    hrReplyField.value = result.reply || "";
    copyReplyButton.disabled = !hrReplyField.value.trim();
    setMessage($("#hr-message-status"), "Черновик готов. Проверьте его перед отправкой.");
  } catch (error) {
    setMessage($("#hr-message-status"), error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = "Подготовить ответ";
  }
});
hrReplyField.addEventListener("input", () => {
  copyReplyButton.disabled = !hrReplyField.value.trim();
});
copyReplyButton.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(hrReplyField.value);
    setMessage($("#hr-message-status"), "Ответ скопирован. Вставьте его в переписку на hh.ru.");
  } catch {
    setMessage($("#hr-message-status"), "Не получилось скопировать. Выделите и скопируйте ответ вручную.", true);
  }
});

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
 projects: $("#projects").value,
 achievements: $("#achievements").value,
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
  $("#application-target").disabled = !event.target.checked;
  $("#application-target-hint").textContent = event.target.checked ? "От 1 до 100. Агент остановится после нужного числа успешных откликов. Если подходящих вакансий меньше, покажет недобор." : "Включите «Отправлять отклики», чтобы задать количество. Поиск без отправки остаётся доступным.";
});

taskForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (cityField.value.trim() && !areaIDField.value) {
    cityHint.textContent = "Выберите город из подсказок или очистите поле.";
    cityHint.classList.add("is-error");
    cityField.focus();
    return;
  }
  runButton.disabled = true;
  const label = runButton.querySelector("span");
  label.textContent = "Запускаю…";
  try {
    await request("/api/run", {
      method: "POST",
      body: JSON.stringify({
        task: $("#task").value,
        areaId: Number(areaIDField.value || 0),
        autoApply: $("#auto-apply").checked,
        applicationTarget: $("#auto-apply").checked ? Number($("#application-target").value) : 0,
        vacancyLimit: Number($("#vacancy-limit").value),
        visible: $("#visible-browser").checked,
      }),
    });
    showActivityMessage("");
    await refreshState();
  } catch (error) {
    showActivityMessage(error.message, true);
  } finally {
    const state = await request("/api/state").catch(() => ({ running: false }));
    runButton.disabled = state.running;
    loginButton.disabled = state.running;
    label.textContent = state.running ? "Выполняется…" : "Начать поиск";
  }
});

loginButton.addEventListener("click", async () => {
  loginButton.disabled = true;
  loginButton.textContent = "Открываю hh.ru…";
  try {
    await request("/api/login", { method: "POST" });
    showActivityMessage("Открыл hh.ru для ручного входа. Поиск не запустится автоматически. Закройте окно hh.ru после авторизации.");
    await refreshState();
  } catch (error) {
    showActivityMessage(error.message, true);
  } finally {
    const state = await request("/api/state").catch(() => ({ running: false }));
    loginButton.disabled = state.running;
    loginButton.textContent = "Только войти на hh.ru";
  }
});

stopButton.addEventListener("click", async () => {
  stopButton.disabled = true;
  stopButton.textContent = "Останавливаю…";
  try {
    await request("/api/cancel", { method: "POST" });
    await refreshState();
  } catch (error) {
    showActivityMessage(error.message, true);
  } finally {
    const state = await request("/api/state").catch(() => ({ running: false }));
    stopButton.disabled = !state.running || /останавливаю/i.test(state.status || "");
    stopButton.textContent = /останавливаю/i.test(state.status || "") ? "Останавливаю…" : "Остановить";
    loginButton.disabled = Boolean(state.running);
  }
});

async function refreshState() {
  const state = await request("/api/state");
  statusText.textContent = state.status || "Готов к поиску";
  statusChip.classList.toggle("is-running", Boolean(state.running));
  statusChip.classList.toggle("is-done", !state.running && state.status === "Поиск завершён");
  statusChip.classList.toggle("is-error", !state.running && /ошибк|не удалось/i.test(state.status || ""));
  runButton.disabled = Boolean(state.running);
  runButton.querySelector("span").textContent = state.running ? "Выполняется…" : "Начать поиск";
  stopButton.hidden = !state.running;
  stopButton.disabled = /останавливаю/i.test(state.status || "");
  stopButton.textContent = stopButton.disabled ? "Останавливаю…" : "Остановить";

  renderVacancyCards(state.cards || [], Boolean(state.running));

  $("#activity-caption").textContent = state.startedAt
    ? `Последний запуск: ${new Date(state.startedAt).toLocaleString("ru-RU")}`
    : "Здесь появится статус и результат последнего запуска";
}

$("#clear-log").addEventListener("click", async () => {
  await request("/api/clear", { method: "POST" }).catch(() => {});
  showActivityMessage("Карточки очищены.");
  renderVacancyCards([], false);
});

loadProfile();
refreshState().catch((error) => {
  statusText.textContent = "Нет связи с локальным агентом";
  statusChip.classList.add("is-error");
  showActivityMessage(error.message, true);
});
window.setInterval(() => refreshState().catch(() => {}), 1200);
