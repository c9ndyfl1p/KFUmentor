const $ = (selector) => document.querySelector(selector);
const taskForm = $("#run-form");
const profileForm = $("#profile-form");
const runButton = $("#run-button");
const matchResumeButton = $("#match-resume-button");
const stopButton = $("#stop-button");
const loginButton = $("#login-button");
const deepseekLoginButton = $("#deepseek-login-button");
const vacancyCards = $("#vacancy-cards");
const activityMessage = $("#activity-message");
const statusChip = $("#status-chip");
const statusText = $("#status-text");
const applyNote = $("#apply-note");
const cityField = $("#city");
const areaIDField = $("#area-id");
const citySuggestions = $("#city-suggestions");
const cityHint = $("#city-hint");
const mentorSteps = $("#mentor-steps");
const mentorGenerateButton = $("#mentor-generate");
let overviewState = null;
let overviewPlan = null;
let overviewProfile = null;
let savedProfileSnapshot = null;

function resizeSearchQuery() {
  const field = $("#task");
  field.style.height = "auto";
  field.style.height = `${Math.min(field.scrollHeight, 240)}px`;
  field.classList.toggle("is-overflowing", field.scrollHeight > 240);
}

$("#task").addEventListener("input", resizeSearchQuery);
resizeSearchQuery();

const sectionForHash = new Map([
  ["overview-section", "overview"], ["overview-title", "overview"],
  ["search-section", "search"], ["search-title", "search"],
  ["task", "search"], ["city", "search"],
  ["activity-section", "search"], ["activity-title", "search"],
  ["hr-section", "hr"], ["hr-title", "hr"],
  ["mentor-section", "mentor"], ["mentor-title", "mentor"],
  ["profile-section", "profile"], ["profile-title", "profile"],
]);

function showAppSection() {
  const section = sectionForHash.get(location.hash.slice(1)) || "overview";
  const workspace = $(".workspace");
  workspace.dataset.activeSection = section;
  document.querySelectorAll("[data-view]").forEach((view) => {
    view.hidden = view.dataset.view !== section;
  });
  document.querySelectorAll("[data-app-section]").forEach((link) => {
    if (link.dataset.appSection === section) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  });
  if (location.hash) {
    requestAnimationFrame(() => document.getElementById(location.hash.slice(1))?.scrollIntoView({ block: "start", behavior: "smooth" }));
  }
}

window.addEventListener("hashchange", showAppSection);
showAppSection();

function selectMentorTab(name, focus = false) {
  const tabs = [...document.querySelectorAll("[data-mentor-tab]")];
  const selected = tabs.find((tab) => tab.dataset.mentorTab === name) || tabs[0];
  if (!selected) return;
  for (const tab of tabs) {
    const active = tab === selected;
    tab.setAttribute("aria-selected", String(active));
    tab.tabIndex = active ? 0 : -1;
  }
  document.querySelectorAll("[data-mentor-panel]").forEach((panel) => {
    panel.hidden = panel.dataset.mentorPanel !== selected.dataset.mentorTab;
  });
  if (focus) selected.focus();
}

document.querySelectorAll("[data-mentor-tab]").forEach((tab) => {
  tab.addEventListener("click", () => selectMentorTab(tab.dataset.mentorTab));
  tab.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const tabs = [...document.querySelectorAll("[data-mentor-tab]")];
    const index = tabs.indexOf(tab);
    const next = event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1
      : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
    selectMentorTab(tabs[next].dataset.mentorTab, true);
  });
});

document.addEventListener("click", (event) => {
  const link = event.target.closest("[data-next-mentor-tab]");
  if (link) selectMentorTab(link.dataset.nextMentorTab);
});

function updateOverview() {
  const state = overviewState;
  const plan = overviewPlan || {};
  const profile = overviewProfile || {};
  const fields = [
    Boolean(profile.name && profile.title),
    Boolean(profile.summary?.trim()),
    Boolean(profile.skills?.length),
    Boolean(profile.experience?.trim() || profile.projects?.trim() || profile.achievements?.trim()),
  ];
  const completion = Math.round(fields.filter(Boolean).length / fields.length * 100);
  $("#overview-profile").textContent = `${completion}%`;
  $("#profile-completion").textContent = `${completion}%`;
  $("#profile-progress-bar").style.width = `${completion}%`;
  $(".profile-progress-track").setAttribute("aria-valuenow", String(completion));
  $("#profile-completion-hint").textContent = completion === 100
    ? "Основные разделы профиля заполнены. Проверьте их актуальность."
    : "Заполните основные сведения, чтобы точнее оценивать подходящие вакансии.";

  const cards = state?.cards || [];
  $("#overview-vacancies").textContent = state?.startedAt
    ? String(cards.filter((card) => card.score > 0 && card.score >= Number(state.minimumScore || 0)).length)
    : "—";
  const applicationMatch = state?.summary?.match(/ОТКЛИКИ:\s*(\d+)/i);
  $("#overview-applications").textContent = state?.startedAt
    ? (applicationMatch ? applicationMatch[1] : "0")
    : "—";
  const turns = plan.interview?.turns || [];
  $("#overview-interview").textContent = String(turns.length);
  const assessment = plan.assessment;
  $("#mentor-career-score").textContent = assessment ? `${assessment.score}/10` : "—";
  $("#mentor-career-score-note").textContent = assessment ? (assessment.level || "Оценка по данным профиля") : "Появится после анализа профиля";
  $("#mentor-assessment-meta").textContent = assessment ? `${assessment.score}/10 · ${assessment.level || "разбор готов"}` : "Критика резюме по существу";
  const questionCount = assessment?.questions?.length || 0;
  $("#mentor-questions-meta").textContent = questionCount ? `${questionCount} ${questionCount === 1 ? "вопрос" : "вопроса"} по вашему опыту` : "Появятся после разбора резюме";
  const steps = plan.steps || [];
  $("#mentor-plan-meta").textContent = steps.length ? `${steps.filter((step) => step.completed).length} из ${steps.length} шагов выполнено` : "План ещё не создан";
  $("#mentor-practice-meta").textContent = plan.exercise?.title || "Задание под ваш уровень";
  $("#mentor-interview-meta").textContent = turns.length ? `${turns.length} ${turns.length === 1 ? "ответ" : "ответа"} сохранено` : "Тренировка с обратной связью";

  const status = $("#overview-agent-status");
  status.textContent = !state?.startedAt
    ? "Поиск ещё не запускался"
    : `${state.running ? "Выполняется" : "Последний статус"}: ${state.status || "Статус недоступен"}`;
  const activity = $("#overview-activity-items");
  activity.replaceChildren();
  const recentCards = [...cards].slice(-3).reverse();
  if (!recentCards.length) {
    const empty = document.createElement("p");
    empty.className = "overview-empty";
    empty.textContent = "Запустите поиск, чтобы увидеть реальные результаты здесь.";
    activity.append(empty);
  } else {
    for (const card of recentCards) {
      const item = document.createElement("article");
      item.className = "overview-activity-item";
      const marker = document.createElement("span");
      marker.className = `activity-marker is-${card.status || "running"}`;
      marker.setAttribute("aria-hidden", "true");
      const detail = document.createElement("div");
      const title = document.createElement(card.url ? "a" : "strong");
      title.className = "overview-item-title";
      title.textContent = card.title || "Вакансия";
      if (card.url) {
        title.href = card.url;
        title.target = "_blank";
        title.rel = "noopener noreferrer";
      }
      const stage = document.createElement("p");
      stage.textContent = [card.stage, card.score ? `${card.score}/10` : ""].filter(Boolean).join(" · ");
      detail.append(title, stage);
      item.append(marker, detail);
      activity.append(item);
    }
  }

  const nextTitle = $("#overview-next-title");
  const nextCopy = $("#overview-next-copy");
  const nextLink = $("#overview-next-link");
  nextLink.removeAttribute("data-next-mentor-tab");
  if (completion < 100) {
    nextTitle.textContent = "Заполните профиль";
    nextCopy.textContent = "Добавьте основные сведения об опыте и навыках, чтобы агент мог сравнивать вакансии с вашей реальной квалификацией.";
    nextLink.href = "#profile-section";
    nextLink.textContent = "Открыть профиль";
  } else if (!plan.assessment) {
    nextTitle.textContent = "Оцените навыки";
    nextCopy.textContent = "Сопоставьте профиль с целевой ролью и узнайте, что стоит подтянуть.";
    nextLink.href = "#mentor-section";
    nextLink.dataset.nextMentorTab = "assessment";
    nextLink.textContent = "Перейти к ментору";
  } else if (!plan.steps?.length) {
    nextTitle.textContent = "Составьте план развития";
    nextCopy.textContent = "Получите практические шаги на ближайшие недели с учётом вашей оценки навыков.";
    nextLink.href = "#mentor-section";
    nextLink.dataset.nextMentorTab = "plan";
    nextLink.textContent = "Открыть план";
  } else if (!plan.exercise) {
    nextTitle.textContent = "Закрепите навык на практике";
    nextCopy.textContent = "Получите задание по выбранной теме и разберите решение с ментором.";
    nextLink.href = "#mentor-section";
    nextLink.dataset.nextMentorTab = "practice";
    nextLink.textContent = "Получить задание";
  } else if (!turns.length) {
    nextTitle.textContent = "Потренируйтесь к собеседованию";
    nextCopy.textContent = "Ответьте на вопросы по целевой роли и получите обратную связь.";
    nextLink.href = "#mentor-section";
    nextLink.dataset.nextMentorTab = "interview";
    nextLink.textContent = "Начать интервью";
  } else {
    nextTitle.textContent = "Продолжите поиск";
    nextCopy.textContent = "Проверьте новые предложения и настройте фильтр соответствия под свои приоритеты.";
    nextLink.href = "#search-section";
    nextLink.textContent = "Перейти к поиску";
  }
}

function renderList(element, items, className = "") {
  element.replaceChildren();
  if (!items?.length) return;
  const list = element.tagName === "UL" ? element : document.createElement("ul");
  if (className) list.className = className;
  for (const item of items) {
    const li = document.createElement("li");
    li.textContent = item;
    list.append(li);
  }
  if (list !== element) element.append(list);
}

function renderMentorPlan(plan) {
  mentorSteps.replaceChildren();
  const steps = plan.steps || [];
  $("#mentor-goal").textContent = plan.goal || "Сохраните профиль и создайте персональный план.";
  $("#mentor-progress-wrap").hidden = !steps.length;
  if (!steps.length) return;

  const completeCount = steps.filter((step) => step.completed).length;
  const percent = Math.round((completeCount / steps.length) * 100);
  $("#mentor-progress-count").textContent = `${completeCount} из ${steps.length} шагов`;
  $("#mentor-progress-percent").textContent = `${percent}%`;
  $("#mentor-progress-bar").style.width = `${percent}%`;
  const progress = $(".mentor-progress-track");
  progress.setAttribute("aria-valuenow", String(percent));

  const summary = document.createElement("p");
  summary.className = "mentor-summary";
  summary.textContent = plan.summary || "";
  mentorSteps.append(summary);

  for (const step of steps) {
    const article = document.createElement("article");
    article.className = "mentor-step";
    article.classList.toggle("is-complete", Boolean(step.completed));

    const label = document.createElement("label");
    label.className = "mentor-step-toggle";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = Boolean(step.completed);
    checkbox.dataset.stepId = step.id;
    checkbox.setAttribute("aria-label", `${step.completed ? "Отметить незавершённым" : "Отметить выполненным"}: ${step.title}`);
    checkbox.addEventListener("change", () => updateMentorStep(step.id, checkbox.checked));
    const visual = document.createElement("span");
    visual.className = "mentor-check";
    visual.setAttribute("aria-hidden", "true");
    label.append(checkbox, visual);

    const content = document.createElement("div");
    content.className = "mentor-step-content";
    const title = document.createElement("h3");
    title.textContent = step.title;
    const action = document.createElement("p");
    action.textContent = step.action;
    const reason = document.createElement("small");
    reason.textContent = step.reason;
    content.append(title, action, reason);
    article.append(label, content);
    mentorSteps.append(article);
  }
}

async function loadMentorPlan() {
  try {
    const plan = await request("/api/mentor/plan");
    overviewPlan = plan;
    updateOverview();
    renderMentorState(plan);
  } catch (error) {
    setMessage($("#mentor-plan-status"), error.message, true);
  }
}

function renderMentorState(state) {
  overviewPlan = state;
  updateOverview();
  renderMentorPlan(state);
  renderMentorAssessment(state.assessment);
  renderMentorExercise(state.exercise);
  renderMentorInterview(state.interview || { turns: [] });
}

function renderMentorAssessment(assessment) {
  if (overviewPlan) overviewPlan.assessment = assessment;
  const target = $("#mentor-assessment-result");
  target.replaceChildren();
  target.hidden = !assessment;
  if (!assessment) {
    renderMentorQuestions(null);
    $("#mentor-questions-status").textContent = "";
    $("#mentor-tailor-actions").hidden = !$("#mentor-vacancy").value.trim();
    return;
  }
  if (assessment.vacancy) $("#mentor-vacancy").value = assessment.vacancy;
  const top = document.createElement("div");
  top.className = "mentor-assessment-score";
  const score = document.createElement("strong");
  score.textContent = `${assessment.score}/10`;
  const level = document.createElement("span");
  level.textContent = assessment.level || "Оценка профиля";
  top.append(score, level);
  const summary = document.createElement("p");
  summary.className = "mentor-summary";
  summary.textContent = assessment.summary || "";
  target.append(top, summary);

  const critiques = assessment.resumeCritiques || [];
  if (critiques.length) {
    const critiqueSection = document.createElement("section");
    critiqueSection.className = "mentor-critique-section";
    const critiqueHeading = document.createElement("h4");
    critiqueHeading.textContent = "Что насторожит работодателя";
    const intro = document.createElement("p");
    intro.className = "mentor-critique-intro";
    intro.textContent = "Красным отмечены точные фрагменты резюме, к которым есть замечания.";
    critiqueSection.append(critiqueHeading, intro);
    const sectionNames = {
      summary: "О себе", skills: "Навыки", experience: "Опыт работы", projects: "Проекты",
      achievements: "Достижения", education: "Образование", languages: "Языки",
    };
    for (const critique of critiques) {
      const card = document.createElement("article");
      card.className = "mentor-critique-card";
      const label = document.createElement("span");
      label.className = "mentor-critique-label";
      label.textContent = sectionNames[critique.section] || "Резюме";
      const quote = document.createElement("blockquote");
      quote.className = "mentor-critique-quote";
      quote.textContent = critique.quote;
      const criticism = document.createElement("p");
      criticism.className = "mentor-critique-reason";
      criticism.textContent = critique.criticism;
      const recommendation = document.createElement("p");
      recommendation.className = "mentor-critique-fix";
      recommendation.textContent = critique.recommendation;
      card.append(label, quote, criticism, recommendation);
      critiqueSection.append(card);
    }
    target.append(critiqueSection);
  }

  for (const [title, items, className] of [
    ["Что уже подтверждено", assessment.strengths, "is-positive"],
    ["Что подтянуть или уточнить", assessment.gaps, "is-gap"],
    ["Следующие действия", assessment.nextSteps, ""],
  ]) {
    if (!items?.length) continue;
    const group = document.createElement("div");
    group.className = `mentor-result-group ${className}`.trim();
    const heading = document.createElement("h4");
    heading.textContent = title;
    group.append(heading);
    renderList(group, items);
    target.append(group);
  }
  renderMentorQuestions(assessment);
  $("#mentor-assess").textContent = "Разобрать резюме";
  const tailorActions = $("#mentor-tailor-actions");
  tailorActions.hidden = !$("#mentor-vacancy").value.trim();
  const tailoredWrap = $("#mentor-tailored-wrap");
  tailoredWrap.hidden = !assessment.tailored;
  if (assessment.tailored) $("#mentor-tailored-draft").value = formatTailoredResume(assessment.tailored);
}

function renderMentorQuestions(assessment) {
  const questions = assessment?.questions || [];
  const answers = assessment?.clarificationAnswers || [];
  $("#mentor-questions-empty").hidden = questions.length > 0;
  $("#mentor-questions-form").hidden = questions.length === 0;
  $("#mentor-questions-meta").textContent = questions.length
    ? `${questions.length} ${questions.length === 1 ? "вопрос" : "вопроса"} по вашему опыту`
    : "Появятся после разбора резюме";
  renderQuestionFields($("#mentor-questions"), questions, answers, "mentor-clarification", 1500, "Ответьте конкретным примером из опыта");
  renderQuestionAnswerHistory($("#mentor-clarification-history"), answers);
}

function renderQuestionFields(container, questions, answers, prefix, maxLength, placeholder) {
  container.replaceChildren();
  const answerByQuestion = new Map((answers || []).map((item) => [item.question, item.answer]));
  questions.forEach((text, index) => {
    const card = document.createElement("article");
    card.className = "mentor-question-card";
    const number = document.createElement("span");
    number.className = "mentor-question-number";
    number.textContent = `ВОПРОС ${index + 1}`;
    const question = document.createElement("p");
    question.className = "mentor-question-text";
    question.textContent = text;
    const label = document.createElement("label");
    const id = `${prefix}-answer-${index}`;
    label.className = "field-label";
    label.htmlFor = id;
    label.textContent = "Ваш ответ";
    const textarea = document.createElement("textarea");
    textarea.id = id;
    textarea.dataset.question = text;
    textarea.maxLength = maxLength;
    textarea.rows = 3;
    textarea.required = true;
    textarea.placeholder = placeholder;
    textarea.value = answerByQuestion.get(text) || "";
    card.append(number, question, label, textarea);
    container.append(card);
  });
}

function renderQuestionAnswerHistory(container, answers) {
  container.replaceChildren();
  if (!answers?.length) return;
  const heading = document.createElement("h4");
  heading.textContent = "Ваши предыдущие ответы";
  const list = document.createElement("div");
  list.className = "mentor-question-history-list";
  for (const item of answers) {
    const card = document.createElement("article");
    card.className = "mentor-question-history-item";
    const question = document.createElement("strong");
    question.textContent = item.question;
    const answerLabel = document.createElement("span");
    answerLabel.className = "mentor-question-answer-label";
    answerLabel.textContent = "ВАШ ОТВЕТ";
    const answer = document.createElement("p");
    answer.textContent = item.answer;
    card.append(question, answerLabel, answer);
    list.append(card);
  }
  container.append(heading, list);
}

function collectQuestionAnswers(container) {
  return [...container.querySelectorAll("textarea[data-question]")]
    .map((textarea) => ({ question: textarea.dataset.question, answer: textarea.value.trim() }))
    .filter((item) => item.question && item.answer);
}

function mergeQuestionAnswers(previous, current, limit = 8) {
  const merged = new Map((previous || []).map((item) => [item.question, item]));
  for (const item of current || []) merged.set(item.question, item);
  return [...merged.values()].slice(-limit);
}

function formatTailoredResume(draft) {
  return [
    draft.headline,
    "",
    "О себе",
    draft.summary,
    "",
    "Навыки",
    (draft.skills || []).join(", "),
    "",
    "Опыт и результаты",
    ...(draft.experience || []).map((item) => `• ${item}`),
    ...(draft.gaps?.length ? ["", "Нужно проверить/уточнить", ...draft.gaps.map((item) => `• ${item}`)] : []),
  ].filter((line, index, all) => line || (index > 0 && all[index - 1])).join("\n");
}

function renderMentorExercise(exercise) {
  const wrapper = $("#mentor-exercise");
  wrapper.hidden = !exercise;
  if (!exercise) return;
  $("#mentor-exercise-title").textContent = exercise.title || "Учебное задание";
  $("#mentor-exercise-prompt").textContent = exercise.prompt || "";
  renderList($("#mentor-exercise-criteria"), exercise.criteria || []);
  const questions = exercise.questions?.length ? exercise.questions : [exercise.prompt || "Опишите решение задания"];
  const answers = exercise.questionAnswers?.length ? exercise.questionAnswers : (exercise.answer ? [{ question: questions[0], answer: exercise.answer }] : []);
  renderQuestionFields($("#mentor-exercise-questions"), questions, answers, "exercise", 2500, "Запишите ход решения именно этого пункта");
  const review = $("#mentor-review");
  review.replaceChildren();
  review.hidden = !exercise.review;
  if (!exercise.review) return;
  const title = document.createElement("h4");
  title.textContent = `Оценка: ${exercise.review.score}/10`;
  const summary = document.createElement("p");
  summary.textContent = exercise.review.summary || "";
  review.append(title, summary);
  for (const [label, items] of [["Уже хорошо", exercise.review.strengths], ["Что улучшить", exercise.review.improvements]]) {
    if (!items?.length) continue;
    const group = document.createElement("div");
    const heading = document.createElement("strong");
    heading.textContent = label;
    group.append(heading);
    renderList(group, items);
    review.append(group);
  }
  if (exercise.review.nextTry) {
    const next = document.createElement("p");
    next.className = "mentor-next-try";
    next.textContent = `Следующая попытка: ${exercise.review.nextTry}`;
    review.append(next);
  }
}

function renderMentorInterview(interview) {
  const current = $("#mentor-interview-current");
  current.replaceChildren();
  current.hidden = !interview.active;
  if (interview.active) {
    const label = document.createElement("span");
    label.className = "mentor-label";
    label.textContent = `ВОПРОС ${interview.turns.length + 1}`;
    const question = document.createElement("p");
    question.className = "mentor-question-text";
    question.textContent = interview.currentQuestion;
    const form = document.createElement("form");
    form.id = "mentor-interview-form";
    const answerLabel = document.createElement("label");
    answerLabel.className = "field-label";
    answerLabel.htmlFor = "mentor-interview-answer";
    answerLabel.textContent = "Ваш ответ";
    const answer = document.createElement("textarea");
    answer.id = "mentor-interview-answer";
    answer.rows = 4;
    answer.maxLength = 6000;
    answer.required = true;
    answer.placeholder = "Опишите, как вы ответили бы на собеседовании: по шагам и с примерами";
    const footer = document.createElement("div");
    footer.className = "mentor-form-footer";
    const submit = document.createElement("button");
    submit.className = "button button-primary";
    submit.id = "mentor-interview-submit";
    submit.type = "submit";
    submit.textContent = "Отправить ответ";
    footer.append(document.createElement("span"), submit);
    form.append(answerLabel, answer, footer);
    current.append(label, question, form);
  }
  $("#mentor-interview-start").hidden = Boolean(interview.active);
  $("#mentor-interview-reset").hidden = !interview.active && !(interview.turns || []).length;
  const turns = $("#mentor-interview-turns");
  turns.replaceChildren();
  for (let i = 0; i < (interview.turns || []).length; i++) {
    const turn = interview.turns[i];
    const article = document.createElement("article");
    article.className = "mentor-interview-turn";
    const question = document.createElement("h4");
    question.textContent = `${i + 1}. ${turn.question}`;
    const answer = document.createElement("p");
    answer.className = "mentor-interview-answer";
    answer.textContent = turn.answer;
    const answerLabel = document.createElement("span");
    answerLabel.className = "mentor-question-answer-label";
    answerLabel.textContent = "ВАШ ОТВЕТ";
    const score = document.createElement("span");
    score.className = "mentor-score-pill";
    score.textContent = `${turn.score}/10`;
    const feedback = document.createElement("p");
    feedback.textContent = turn.feedback;
    article.append(question, answerLabel, answer, score, feedback);
    for (const [title, items] of [["Сильные стороны", turn.strengths], ["Что улучшить", turn.improvements]]) {
      if (!items?.length) continue;
      const group = document.createElement("div");
      const heading = document.createElement("strong");
      heading.textContent = title;
      group.append(heading);
      renderList(group, items);
      article.append(group);
    }
    turns.append(article);
  }
  if (!interview.active && (interview.turns || []).length >= 6) {
    const done = document.createElement("p");
    done.className = "mentor-status";
    done.textContent = "Тренировка завершена. Посмотрите оценки и рекомендации по ответам выше.";
    turns.append(done);
  }
}

mentorGenerateButton.addEventListener("click", async () => {
  mentorGenerateButton.disabled = true;
  mentorGenerateButton.textContent = "Составляю план…";
  setMessage($("#mentor-plan-status"), "Ментор анализирует профиль и готовит конкретные шаги. Это может занять до пары минут.");
  try {
    renderMentorState(await request("/api/mentor/plan", { method: "POST" }));
    setMessage($("#mentor-plan-status"), "План готов и сохранён на этом компьютере.");
  } catch (error) {
    setMessage($("#mentor-plan-status"), error.message, true);
  } finally {
    mentorGenerateButton.disabled = false;
    mentorGenerateButton.textContent = "Обновить план";
  }
});

async function updateMentorStep(id, completed) {
  try {
    const plan = await request("/api/mentor/step", {
      method: "POST",
      body: JSON.stringify({ id, completed }),
    });
    renderMentorPlan(plan);
    mentorSteps.querySelector(`[data-step-id="${CSS.escape(id)}"]`)?.focus();
    setMessage($("#mentor-plan-status"), "Прогресс сохранён.");
  } catch (error) {
    setMessage($("#mentor-plan-status"), error.message, true);
    await loadMentorPlan();
  }
}

$("#mentor-assessment-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#mentor-assess");
  button.disabled = true;
  setMessage($("#mentor-assessment-status"), "Проверяю резюме глазами работодателя и сверяю точные формулировки…");
  try {
    const assessment = await request("/api/mentor/assessment", {
      method: "POST",
    body: JSON.stringify({
      vacancy: $("#mentor-vacancy").value,
      answers: overviewPlan?.assessment?.clarificationAnswers || [],
    }),
    });
    renderMentorAssessment(assessment);
    updateOverview();
    setMessage($("#mentor-assessment-status"), "Разбор сохранён локально. Замечания привязаны к точным фрагментам резюме.");
  } catch (error) {
    setMessage($("#mentor-assessment-status"), error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = "Разобрать резюме";
  }
});

$("#mentor-questions-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#mentor-answer-questions");
  const answers = mergeQuestionAnswers(
    overviewPlan?.assessment?.clarificationAnswers,
    collectQuestionAnswers($("#mentor-questions")),
  );
  button.disabled = true;
  setMessage($("#mentor-questions-status"), "Учитываю ваши ответы и пересматриваю разбор…");
  try {
    const assessment = await request("/api/mentor/assessment", {
      method: "POST",
      body: JSON.stringify({ vacancy: $("#mentor-vacancy").value, answers }),
    });
    renderMentorAssessment(assessment);
    if (overviewPlan) overviewPlan.assessment = assessment;
    updateOverview();
    setMessage($("#mentor-questions-status"), "Ответы учтены. Разбор резюме обновлён.");
  } catch (error) {
    setMessage($("#mentor-questions-status"), error.message, true);
  } finally {
    button.disabled = false;
  }
});

$("#mentor-reset-history").addEventListener("click", async (event) => {
  const button = event.currentTarget;
  if (!window.confirm("Сбросить историю ментора? Удалятся разбор резюме, план, задания и ответы собеседования.")) return;
  button.disabled = true;
  button.textContent = "Сбрасываю…";
  try {
    const plan = await request("/api/mentor/plan", { method: "DELETE" });
    $("#mentor-vacancy").value = "";
    $("#mentor-tailored-draft").value = "";
    $("#mentor-tailored-wrap").hidden = true;
    $("#mentor-tailor-actions").hidden = true;
    setMessage($("#mentor-assessment-status"), "История ментора сброшена.");
    renderMentorState(plan);
  } catch (error) {
    setMessage($("#mentor-assessment-status"), error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = "Сбросить историю";
  }
});

$("#mentor-vacancy").addEventListener("input", () => {
  $("#mentor-tailor-actions").hidden = !$("#mentor-vacancy").value.trim();
});

$("#mentor-tailor-resume").addEventListener("click", async () => {
  const button = $("#mentor-tailor-resume");
  button.disabled = true;
  button.textContent = "Адаптирую…";
  setMessage($("#mentor-tailor-status"), "Сверяю требования вакансии с подтверждённым опытом…");
  try {
    const draft = await request("/api/mentor/resume", {
      method: "POST",
      body: JSON.stringify({ vacancy: $("#mentor-vacancy").value }),
    });
    $("#mentor-tailored-draft").value = formatTailoredResume(draft);
    $("#mentor-tailored-wrap").hidden = false;
    setMessage($("#mentor-tailor-status"), "Черновик сохранён локально. Проверьте все факты перед использованием.");
  } catch (error) {
    setMessage($("#mentor-tailor-status"), error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = "Адаптировать резюме под вакансию";
  }
});

$("#mentor-copy-tailored").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText($("#mentor-tailored-draft").value);
    setMessage($("#mentor-tailor-status"), "Черновик скопирован.");
  } catch {
    setMessage($("#mentor-tailor-status"), "Не получилось скопировать. Выделите черновик и скопируйте вручную.", true);
  }
});

$("#mentor-exercise-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#mentor-create-exercise");
  button.disabled = true;
  button.textContent = "Подбираю…";
  try {
    renderMentorExercise(await request("/api/mentor/exercise", {
      method: "POST",
      body: JSON.stringify({ topic: $("#mentor-topic").value }),
    }));
    setMessage($("#mentor-exercise-status"), "Задание создано и сохранено.");
  } catch (error) {
    setMessage($("#mentor-exercise-status"), error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = "Получить задание";
  }
});

$("#mentor-exercise-answer-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#mentor-review-answer");
  button.disabled = true;
  button.textContent = "Разбираю…";
  try {
    renderMentorExercise(await request("/api/mentor/exercise/review", {
      method: "POST",
      body: JSON.stringify({ answers: collectQuestionAnswers($("#mentor-exercise-questions")) }),
    }));
    setMessage($("#mentor-exercise-status"), "Обратная связь сохранена.");
  } catch (error) {
    setMessage($("#mentor-exercise-status"), error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = "Разобрать ответ";
  }
});

async function mentorInterview(action, answer = "") {
  const button = action === "answer" ? $("#mentor-interview-submit") : $("#mentor-interview-start");
  button.disabled = true;
  const original = button.textContent;
  button.textContent = action === "answer" ? "Оцениваю…" : "Готовлю вопрос…";
  setMessage($("#mentor-interview-status"), action === "answer" ? "Разбираю ответ и готовлю следующий вопрос…" : "Подбираю первый вопрос по вашей роли…");
  try {
    await request("/api/mentor/interview", { method: "POST", body: JSON.stringify({ action, answer }) });
    const state = await request("/api/mentor/plan");
    renderMentorState(state);
    setMessage($("#mentor-interview-status"), action === "answer" ? "Ответ оценён и сохранён." : "Интервью началось.");
    if (action === "answer" && state.interview?.active) $("#mentor-interview-answer").focus();
  } catch (error) {
    setMessage($("#mentor-interview-status"), error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = original;
  }
}

$("#mentor-interview-start").addEventListener("click", () => mentorInterview("start"));
$("#mentor-interview-current").addEventListener("submit", (event) => {
  if (event.target.id !== "mentor-interview-form") return;
  event.preventDefault();
  mentorInterview("answer", $("#mentor-interview-answer").value);
});
$("#mentor-interview-reset").addEventListener("click", async () => {
  try {
    await request("/api/mentor/interview", { method: "POST", body: JSON.stringify({ action: "reset" }) });
    renderMentorState(await request("/api/mentor/plan"));
    setMessage($("#mentor-interview-status"), "Интервью завершено.");
  } catch (error) {
    setMessage($("#mentor-interview-status"), error.message, true);
  }
});

let citySearchTimer;
let citySearchController;
let activeCityOption = -1;
let hhCaptchaRequired = false;
let deepSeekCaptchaRequired = false;

function syncCaptchaWarning() {
  const dialog = $("#captcha-dialog");
  const required = hhCaptchaRequired || deepSeekCaptchaRequired;
  if (required && !dialog.open) dialog.showModal();
  if (!required && dialog.open) dialog.close();
  const description = $("#captcha-description");
  if (hhCaptchaRequired && deepSeekCaptchaRequired) {
    description.textContent = "Завершите проверку в окне hh.ru и в окне DeepSeek. Поиск продолжится автоматически после прохождения капч.";
  } else if (deepSeekCaptchaRequired) {
    description.textContent = "Завершите проверку в открытом окне DeepSeek. Запрос модели ждёт, а поиск продолжится автоматически после прохождения капчи.";
  } else {
    description.textContent = "Откройте окно hh.ru и завершите проверку. Поиск приостановлен и продолжится автоматически, когда капча будет пройдена.";
  }
}

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

function renderVacancyCards(cards, running, minimumScore = 0) {
  vacancyCards.replaceChildren();
  const matchingCards = cards.filter((item) => {
    const score = Number(item.score || 0);
    if (score > 0) return score >= minimumScore;
    return running && ["running", "queued"].includes(item.status);
  });
  if (!matchingCards.length) {
    const empty = document.createElement("p");
    empty.className = "activity-empty";
    if (running && minimumScore > 0) {
      empty.textContent = `Проверяю вакансии. Покажу ход анализа и результаты от ${minimumScore}/10.`;
    } else if (running) {
      empty.textContent = "Ищу вакансии. Карточки появятся, когда агент начнёт их оценивать.";
    } else if (cards.length && minimumScore > 0) {
      empty.textContent = `Пока нет вакансий с оценкой от ${minimumScore}/10. Попробуйте снизить порог или изменить запрос.`;
    } else {
      empty.textContent = "Здесь появятся карточки подходящих вакансий.";
    }
    vacancyCards.append(empty);
    return;
  }

  for (const item of matchingCards) {
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
    state.textContent = ({ running: "В работе", applying: "Отклик", success: "Готово", skipped: "Пропущена", stopped: "Остановлено", error: "Ошибка", review: "Ручная проверка", queued: "В очереди", ready: "Письмо готово", recommended: "Рекомендована", warning: "Требует внимания" })[item.status] || "В работе";
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
    if(window.renderManualActions) window.renderManualActions(item,card);
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
  overviewProfile = profile;
  savedProfileSnapshot = structuredClone(profile || {});
  $("#name").value = profile.name || "";
  $("#title").value = profile.title || "";
  $("#summary").value = profile.summary || "";
  $("#skills").value = (profile.skills || []).join(", ");
  $("#experience").value = profile.experience || "";
 $("#projects").value = profile.projects || "";
 $("#achievements").value = profile.achievements || "";
  if (!$("#hr-position").value) $("#hr-position").value = profile.title || "";
  renderProfilePreview(profile);
  updateOverview();
}

function renderProfilePreview(profile = {}) {
  $("#profile-preview-name").textContent = profile.name?.trim() || "Ваше имя";
  $("#profile-preview-title").textContent = profile.title?.trim() || "Желаемая должность";
  $("#profile-preview-summary").textContent = profile.summary?.trim() || "Коротко расскажите о своём опыте — это поможет ментору и поиску вакансий.";
  const skills = Array.isArray(profile.skills) ? profile.skills : String(profile.skills || "").split(",");
  const tags = $("#profile-preview-skills");
  tags.replaceChildren();
  const cleanSkills = skills.map((skill) => String(skill).trim()).filter(Boolean);
  if (!cleanSkills.length) {
    const empty = document.createElement("span");
    empty.className = "profile-empty";
    empty.textContent = "Добавьте навыки в профиль";
    tags.append(empty);
  } else {
    for (const skill of cleanSkills) {
      const tag = document.createElement("span");
      tag.className = "profile-skill-tag";
      tag.textContent = skill;
      tags.append(tag);
    }
  }
  $("#profile-preview-experience").textContent = profile.experience?.trim() || "Добавьте места работы и результаты.";
  $("#profile-preview-projects").textContent = profile.projects?.trim() || "Добавьте учебные или личные проекты.";
  $("#profile-preview-achievements").textContent = profile.achievements?.trim() || "Добавьте результаты и достижения.";
}

function setProfileEditing(editing) {
  $("#profile-form").hidden = !editing;
  $("#profile-read-view").hidden = editing;
  if (editing) $("#name").focus();
  else $("#edit-profile-button").focus();
}

$("#edit-profile-button").addEventListener("click", () => setProfileEditing(true));
$("#cancel-profile-edit").addEventListener("click", () => { fillProfile(savedProfileSnapshot || {}); setProfileEditing(false); });
$("#cancel-profile-edit-bottom").addEventListener("click", () => { fillProfile(savedProfileSnapshot || {}); setProfileEditing(false); });

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
    $("#regenerate-reply").disabled = !hrReplyField.value.trim();
    $("#hr-draft-insight").classList.add("has-draft");
    $("#hr-draft-insight p").textContent = "Черновик составлен на основе вашего профиля и сообщения HR. Проверьте факты, детали и тон перед отправкой.";
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
  $("#regenerate-reply").disabled = !hrReplyField.value.trim();
});
$("#regenerate-reply").addEventListener("click", () => hrReplyForm.requestSubmit());
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
    overviewProfile = data;
    savedProfileSnapshot = structuredClone(data);
    renderProfilePreview(data);
    updateOverview();
    setMessage($("#profile-message"), result.status);
    setProfileEditing(false);
  } catch (error) {
    setMessage($("#profile-message"), error.message, true);
  } finally {
    button.disabled = false;
  }
});

profileForm.addEventListener("input", () => {
  overviewProfile = {
    name: $("#name").value,
    title: $("#title").value,
    summary: $("#summary").value,
    skills: $("#skills").value.split(",").map((skill) => skill.trim()).filter(Boolean),
    experience: $("#experience").value,
    projects: $("#projects").value,
    achievements: $("#achievements").value,
  };
  renderProfilePreview(overviewProfile);
  updateOverview();
});

$("#auto-apply").addEventListener("change", (event) => {
  applyNote.hidden = !event.target.checked;
  $("#application-target").disabled = !event.target.checked;

  $("#application-target-hint").textContent = event.target.checked ? "От 1 до 100. Агент остановится после нужного числа успешных откликов. Если подходящих вакансий меньше, покажет недобор." : "Включите «Отправлять отклики», чтобы задать количество. Поиск без отправки остаётся доступным.";
});

async function launchSearch(task, profileMatch = false) {
  if (cityField.value.trim() && !areaIDField.value) {
    cityHint.textContent = "Выберите город из подсказок или очистите поле.";
    cityHint.classList.add("is-error");
    cityField.focus();
    return;
  }
  runButton.disabled = true;
  matchResumeButton.disabled = true;
  const label = runButton.querySelector("span");
  label.textContent = "Запускаю…";
  try {
    await request("/api/run", {
      method: "POST",
      body: JSON.stringify({
        task,
        areaId: Number(areaIDField.value || 0),
        autoApply: !profileMatch && $("#auto-apply").checked,
 applyByScore: !$("#application-policy").checked,
        applicationTarget: !profileMatch && $("#auto-apply").checked ? Number($("#application-target").value) : 0,
        vacancyLimit: Number($("#vacancy-limit").value),
        minimumScore: Number($("#minimum-score").value),
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
    matchResumeButton.disabled = state.running;
    loginButton.disabled = state.running;
    deepseekLoginButton.disabled = state.running;
    label.textContent = state.running ? "Выполняется…" : "Начать поиск";
  }
}

taskForm.addEventListener("submit", (event) => {
  event.preventDefault();
  launchSearch($("#task").value);
});

matchResumeButton.addEventListener("click", () => {
  const profile = savedProfileSnapshot || {};
  const skills = Array.isArray(profile.skills) ? profile.skills : [];
  const hasResume = Boolean(profile.title?.trim() || profile.summary?.trim() || skills.length || profile.experience?.trim() || profile.projects?.trim());
  if (!hasResume) {
    setMessage($("#profile-message"), "Сначала прикрепите резюме или заполните профиль и сохраните его.", true);
    location.hash = "profile-section";
    return;
  }
  const role = profile.title?.trim();
  const skillsText = skills.slice(0, 4).join(" ");
  const searchDirection = role || skillsText || profile.summary?.trim() || profile.experience?.trim();
  if (!searchDirection) {
    setMessage($("#profile-message"), "Добавьте желаемую должность или заполните навыки в профиле.", true);
    location.hash = "profile-section";
    return;
  }
  launchSearch(searchDirection, true);
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
    loginButton.textContent = "Войти ↗";
  }
});

deepseekLoginButton.addEventListener("click", async () => {
  deepseekLoginButton.disabled = true;
  deepseekLoginButton.textContent = "Открываю DeepSeek…";
  try {
    await request("/api/deepseek/login", { method: "POST" });
    showActivityMessage("Открыл окно DeepSeek. Войдите в аккаунт вручную; поиск не запустится.");
  } catch (error) {
    showActivityMessage(error.message, true);
  } finally {
    const state = await request("/api/state").catch(() => ({ running: false }));
    deepseekLoginButton.disabled = state.running;
    deepseekLoginButton.textContent = "Открыть ↗";
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
    deepseekLoginButton.disabled = Boolean(state.running);
  }
});

async function refreshState() {
  const state = await request("/api/state");
  hhCaptchaRequired = Boolean(state.captchaRequired);
  syncCaptchaWarning();
  statusText.textContent = state.status || "Готов к поиску";
  statusChip.classList.toggle("is-running", Boolean(state.running));
  statusChip.classList.toggle("is-done", !state.running && state.status === "Поиск завершён");
  statusChip.classList.toggle("is-error", !state.running && /ошибк|не удалось/i.test(state.status || ""));
  runButton.disabled = Boolean(state.running);
  matchResumeButton.disabled = Boolean(state.running);
  runButton.querySelector("span").textContent = state.running ? "Выполняется…" : "Начать поиск";
  loginButton.disabled = Boolean(state.running);
  deepseekLoginButton.disabled = Boolean(state.running);
  stopButton.hidden = !state.running;
  stopButton.disabled = /останавливаю/i.test(state.status || "");
  stopButton.textContent = stopButton.disabled ? "Останавливаю…" : "Остановить";

  const summary = $("#task-summary");
  summary.textContent = state.summary || "";
  summary.hidden = !state.summary;

  renderVacancyCards(state.cards || [], Boolean(state.running), Number(state.minimumScore || 0));
  overviewState = state;
  updateOverview();

  $("#activity-caption").textContent = state.startedAt
    ? `Последний запуск: ${new Date(state.startedAt).toLocaleString("ru-RU")}`
    : "Здесь появится статус и результат последнего запуска";
}

async function refreshDeepSeekStatus() {
  try {
    const state = await request("/api/deepseek/status");
    deepSeekCaptchaRequired = Boolean(state.captchaRequired);
  } catch {
    deepSeekCaptchaRequired = false;
  }
  syncCaptchaWarning();
}

$("#clear-log").addEventListener("click", async () => {
  await request("/api/clear", { method: "POST" }).catch(() => {});
  showActivityMessage("Карточки очищены.");
  renderVacancyCards([], false);
});

loadProfile();
loadMentorPlan();
refreshState().catch((error) => {
  statusText.textContent = "Нет связи с локальным агентом";
  statusChip.classList.add("is-error");
  showActivityMessage(error.message, true);
});
window.setInterval(() => refreshState().catch(() => {}), 1200);
window.setInterval(() => refreshDeepSeekStatus(), 2000);
