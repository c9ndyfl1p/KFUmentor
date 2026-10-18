package browser

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
)

type AIOrchestrator struct {
	llmClient         *llm.OllamaClient
	page              playwright.Page
	context           playwright.BrowserContext
	debug             bool
	currentTask       string
	actionHistory     []map[string]interface{}
	resume            types.Resume
	autoApply         bool
	areaID            int
	lastEvaluatedJobs []types.Job
}

type AIAction struct {
	Action     string                 `json:"action"`
	Parameters map[string]interface{} `json:"parameters"`
	Reason     string                 `json:"reason"`
	NextStep   string                 `json:"next_step,omitempty"`
}

type JobEvaluationResult struct {
	IsRelevant     bool     `json:"is_relevant"`
	Reason         string   `json:"reason"`
	MatchScore     int      `json:"match_score"`
	Recommendation string   `json:"recommendation"`
	Analysis       string   `json:"analysis"`
	SkillsMatch    []string `json:"skills_match"`
	Requirements   []string `json:"requirements"`
}

func CreateLLMOrchestrator(modelURL, modelName, resumePath string, page playwright.Page, ctx playwright.BrowserContext, debug bool) *AIOrchestrator {
	client := llm.NewOllamaClient(modelURL, modelName)
	resume := loadResume(resumePath)

	return &AIOrchestrator{
		llmClient:     client,
		page:          page,
		context:       ctx,
		debug:         debug,
		actionHistory: []map[string]interface{}{},
		resume:        resume,
	}
}

func (a *AIOrchestrator) ExecuteTask(task string, autoApply bool) (string, error) {
	log.Printf("🤖 AI Agent processing task: %s (autoApply: %v)", task, autoApply)
	if autoApply && (strings.TrimSpace(a.resume.Name) == "" || strings.TrimSpace(a.resume.Title) == "" || len(a.resume.Skills) == 0) {
		return "", fmt.Errorf("автоотклики требуют заполненного профиля кандидата: укажите name, title и skills в файле резюме")
	}
	a.autoApply = autoApply
	a.currentTask = task

	plan, err := a.createPlan(task, autoApply)
	if err != nil {
		return "", fmt.Errorf("plan creation failed: %w", err)
	}

	log.Printf("📋 AI создал план из %d шагов", len(plan))

	result, err := a.executePlan(plan)
	if err != nil {
		return "", fmt.Errorf("plan execution failed: %w", err)
	}

	finalReport := a.generateFinalReport()

	return finalReport + "\n\n" + result, nil
}

func (a *AIOrchestrator) createPlan(task string, autoApply bool) ([]AIAction, error) {
	query := extractQueryFromTask(task)
	count := 5
	if match := regexp.MustCompile(`(?i)\b(\d{1,2})\b`).FindStringSubmatch(task); len(match) == 2 {
		if parsed, err := strconv.Atoi(match[1]); err == nil && parsed > 0 {
			count = min(parsed, 20)
		}
	}
	plan := []AIAction{
		{Action: "search", Parameters: map[string]interface{}{"query": query}, Reason: "Искать вакансии по запросу"},
		{Action: "extract_jobs", Parameters: map[string]interface{}{"count": float64(count)}, Reason: fmt.Sprintf("Извлечь до %d вакансий", count)},
		{Action: "evaluate_jobs", Parameters: map[string]interface{}{}, Reason: "Оценить найденные вакансии по профилю"},
	}
	if autoApply {
		plan = append(plan, AIAction{Action: "apply_to_relevant", Parameters: map[string]interface{}{}, Reason: "Подготовить отклики на подходящие вакансии"})
	}
	return plan, nil
}

func (a *AIOrchestrator) executePlan(plan []AIAction) (string, error) {
	var results []string

	for i, action := range plan {
		log.Printf("🔧 Шаг %d/%d: %s", i+1, len(plan), action.Reason)

		result, err := a.executeAction(action)
		if err != nil {
			log.Printf("⚠️ Ошибка на шаге %d: %v", i+1, err)
			results = append(results, fmt.Sprintf("❌ Шаг %d: %s - Ошибка: %v",
				i+1, action.Reason, err))
			// A failed search leaves the previous page open. Never extract jobs
			// from that page or continue toward applications after navigation fails.
			if action.Action == "search" {
				return strings.Join(results, "\n"), err
			}
			continue
		}

		// Записываем в историю
		a.actionHistory = append(a.actionHistory, map[string]interface{}{
			"step":      i + 1,
			"action":    action.Action,
			"reason":    action.Reason,
			"result":    result,
			"timestamp": time.Now().Format(time.RFC3339),
		})

		results = append(results, fmt.Sprintf("✅ Шаг %d: %s - %s",
			i+1, action.Reason, result))

		time.Sleep(1 * time.Second)
	}

	return strings.Join(results, "\n"), nil
}

func (a *AIOrchestrator) executeAction(action AIAction) (string, error) {
	switch action.Action {
	case "navigate":
		url, ok := action.Parameters["url"].(string)
		if !ok {
			return "", fmt.Errorf("URL not provided for navigation")
		}
		return a.navigate(url)

	case "search":
		query, ok := action.Parameters["query"].(string)
		if !ok {
			query = extractQueryFromTask(a.currentTask)
		}
		return a.searchJobs(query)

	case "extract_jobs":
		count := 10 // default
		if val, ok := action.Parameters["count"]; ok && val != nil {
			count = int(val.(float64))
		}
		jobs, err := a.extractJobsFromPage(count)
		if err != nil {
			return "", err
		}
		a.lastEvaluatedJobs = jobs
		return fmt.Sprintf("Извлечено %d вакансий", len(jobs)), nil

	case "evaluate_jobs":
		return a.evaluateAllJobsOnPage()

	case "apply_to_relevant":
		if !a.autoApply {
			return "Отклики не отправлялись: для отправки требуется явно включить --auto-apply", nil
		}
		return a.applyToRelevantJobs()

	case "analyze_page":
		return a.analyzePage()

	case "check_login":
		return a.checkLogin()

	case "solve_captcha":
		return a.handleCaptcha()

	default:
		return "", fmt.Errorf("unsupported action %q", action.Action)
	}
}

func (a *AIOrchestrator) applyToRelevantJobs() (string, error) {
	log.Println("📨 Откликаюсь на релевантные вакансии...")

	if len(a.lastEvaluatedJobs) == 0 {
		log.Println("ℹ️ Нет оцененных вакансий, извлекаю 5 вакансий...")
		jobs, err := a.extractJobsFromPage(5)
		if err != nil {
			return "", fmt.Errorf("не удалось извлечь вакансии: %w", err)
		}
		a.lastEvaluatedJobs = jobs
	}

	appliedCount := 0
	skippedCount := 0
	errorCount := 0

	var results []string

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🧠 НАЧИНАЮ АНАЛИЗ ВАКАНСИЙ И АВТООТКЛИКИ")
	fmt.Println(strings.Repeat("=", 80))

	for i, job := range a.lastEvaluatedJobs {
		log.Printf("🤔 Анализирую вакансию %d/%d: %s",
			i+1, len(a.lastEvaluatedJobs), job.Title)

		if !strings.Contains(job.URL, "/vacancy/") || job.URL == "" {
			log.Printf("⚠️ Пропускаем невалидную ссылку: %s", job.URL)
			skippedCount++
			continue
		}

		log.Println("🧠 Запрашиваю анализ у AI модели...")
		evaluation, err := a.evaluateJobForTaskWithAnalysis(job, a.currentTask)
		if err != nil {
			log.Printf("❌ Ошибка анализа: %v", err)
			results = append(results, fmt.Sprintf("❌ %s: ошибка анализа", job.Title))
			errorCount++
			continue
		}

		a.printJobAnalysis(job, evaluation)

		shouldApply := evaluation.IsRelevant && evaluation.MatchScore >= 7

		if shouldApply {
			log.Printf("✅ Решение: откликнуться (оценка: %d/10)", evaluation.MatchScore)

			applyResult, err := a.performJobApplication(job)
			if err != nil {
				log.Printf("❌ Отклик не подтверждён: %v", err)
				results = append(results, fmt.Sprintf("❌ %s: %v", job.Title, err))
				errorCount++
				// A navigation/interaction error can mean the browser is no longer
				// on the expected vacancy. Stop automatic submissions for this run.
				break
			} else {
				appliedCount++
				results = append(results, fmt.Sprintf("✅ %s: отклик отправлен!", job.Title))
				log.Printf("🎉 Отклик отправлен! Результат: %s", applyResult)
			}
		} else {
			log.Printf("⏭️ Решение: пропустить (оценка: %d/10)", evaluation.MatchScore)
			skippedCount++
			results = append(results, fmt.Sprintf("⏭️ %s: оценка %d/10",
				job.Title, evaluation.MatchScore))
		}

		fmt.Println()
		time.Sleep(3 * time.Second)
	}

	// Финальный отчет
	finalResult := fmt.Sprintf(`
📊 ИТОГ АНАЛИЗА ВАКАНСИЙ
%s
Проанализировано: %d вакансий
Отправлено откликов: %d
Пропущено: %d
Ошибок: %d
%s
%s`,
		strings.Repeat("=", 50),
		len(a.lastEvaluatedJobs),
		appliedCount,
		skippedCount,
		errorCount,
		strings.Repeat("=", 50),
		strings.Join(results, "\n"))

	fmt.Println(finalResult)
	return finalResult, nil
}

func (a *AIOrchestrator) performJobApplication(job types.Job) (string, error) {
	log.Printf("📝 Начинаю отклик на вакансию: %s", job.Title)

	page, err := a.context.NewPage()
	if err != nil {
		return "", fmt.Errorf("не удалось создать страницу: %w", err)
	}
	defer page.Close()

	log.Printf("🌐 Перехожу на: %s", job.URL)
	_, err = page.Goto(job.URL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(30000),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		return "", fmt.Errorf("не удалось перейти на вакансию: %w", err)
	}

	time.Sleep(2 * time.Second)

	title, _ := page.Locator("h1").First().TextContent()
	companyLocator := page.Locator("[data-qa='vacancy-company-name'], .vacancy-company-name")
	company, _ := companyLocator.First().TextContent()
	vacancyDescription := job.Snippet
	for _, selector := range []string{"[data-qa='vacancy-description']", ".vacancy-description"} {
		loc := page.Locator(selector).First()
		if count, _ := loc.Count(); count > 0 {
			if content, err := loc.InnerText(); err == nil && strings.TrimSpace(content) != "" {
				vacancyDescription = content
				break
			}
		}
	}
	vacancyDescription = truncateRunes(strings.Join(strings.Fields(vacancyDescription), " "), 1800)

	coverLetter, err := a.generateCoverLetter(title, company, vacancyDescription)
	if err != nil {
		log.Printf("⚠️ Не удалось сгенерировать письмо: %v", err)
		coverLetter = "Здравствуйте! Заинтересован(а) в этой вакансии. Готов(а) обсудить детали. С уважением."
	}
	log.Printf("✉️ Сопроводительное письмо:\n%s", coverLetter)

	applyBtnSelectors := []string{
		"button:has-text('Откликнуться')",
		"[data-qa='vacancy-response-link-top']",
		"[data-qa='vacancy-serp__vacancy_response']",
		".bloko-button:has-text('Откликнуться')",
	}

	var applyBtn playwright.Locator
	for _, selector := range applyBtnSelectors {
		locator := page.Locator(selector)
		if count, _ := locator.Count(); count > 0 {
			applyBtn = locator.First()
			break
		}
	}

	if applyBtn == nil {
		return "", fmt.Errorf("кнопка отклика не найдена")
	}

	log.Println("🖱️ Нажимаю кнопку 'Откликнуться'...")
	if err := applyBtn.Click(); err != nil {
		return "", fmt.Errorf("не удалось нажать кнопку отклика: %w", err)
	}

	time.Sleep(2 * time.Second)

	messageFieldSelectors := []string{
		"textarea[data-qa='vacancy-response-popup-form-letter-input']",
		"textarea[name='message']",
		"textarea",
		"[contenteditable='true']",
	}
	submitBtnSelectors := []string{
		"button:has-text('Отправить отклик')",
		"button:has-text('Отправить')",
		"[data-qa='vacancy-response-submit-popup']",
		"[data-qa='vacancy-response-submit']",
	}

	letterFilled := false
	for _, selector := range messageFieldSelectors {
		field := page.Locator(selector)
		if count, _ := field.Count(); count > 0 {
			first := field.First()
			visible, _ := first.IsVisible()
			if visible && first.Fill(coverLetter) == nil {
				log.Println("✅ Сопроводительное письмо заполнено")
				letterFilled = true
				break
			}
		}
	}
	if !letterFilled {
		// HH can accept an application first and offer a separate
		// "Добавить сопроводительное" action on the resulting response page.
		// Support that flow instead of silently treating a bare response as a
		// completed application with a letter.
		log.Println("ℹ️ Поля письма в форме отклика нет; отправляю отклик и ищу «Добавить сопроводительное»")
		if err := clickVisible(page, submitBtnSelectors); err != nil {
			return "", fmt.Errorf("поле сопроводительного не найдено, не удалось отправить отклик: %w", err)
		}
		if err := waitForAnyVisible(page, []string{
			"[data-qa='vacancy-response-popup-success']",
			"[data-qa='vacancy-response-success']",
			"button:has-text('Вы откликнулись')",
			"text=Отклик отправлен",
		}, 8*time.Second); err != nil {
			return "", fmt.Errorf("hh.ru не подтвердил отклик без письма; проверьте «Мои отклики» перед повтором: %w", err)
		}

		if err := clickVisible(page, []string{
			"button:has-text('Добавить сопроводительное')",
			"text=Добавить сопроводительное",
		}); err != nil {
			return "", fmt.Errorf("отклик отправлен без письма; кнопка «Добавить сопроводительное» не найдена: %w", err)
		}
		time.Sleep(time.Second)
		if err := fillVisibleMessage(page, coverLetter); err != nil {
			return "", fmt.Errorf("отклик отправлен без письма; после «Добавить сопроводительное» поле не найдено: %w", err)
		}
		if err := clickVisible(page, []string{
			"button:has-text('Отправить')",
			"button:has-text('Добавить')",
			"button:has-text('Сохранить')",
			"[data-qa='vacancy-response-submit-popup']",
		}); err != nil {
			return "", fmt.Errorf("отклик отправлен, письмо заполнено, но не удалось отправить сопроводительное: %w", err)
		}
		return fmt.Sprintf("Отклик и сопроводительное отправлены для '%s' в '%s'", title, company), nil
	}

	for _, selector := range submitBtnSelectors {
		submitBtn := page.Locator(selector)
		if count, _ := submitBtn.Count(); count > 0 {
			button := submitBtn.First()
			if visible, _ := button.IsVisible(); !visible {
				continue
			}
			if err := button.Click(); err != nil {
				log.Printf("⚠️ Не удалось нажать %s: %v", selector, err)
				continue
			}

			log.Println("📤 Нажата кнопка отправки; жду подтверждение hh.ru...")
			successSelectors := []string{
				"[data-qa='vacancy-response-popup-success']",
				"[data-qa='vacancy-response-success']",
				"button:has-text('Вы откликнулись')",
				"text=Вы откликнулись",
				"text=Отклик отправлен",
				"text=Отклик успешно отправлен",
			}
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				for _, successSel := range successSelectors {
					if count, _ := page.Locator(successSel).Count(); count > 0 {
						return fmt.Sprintf("Отклик подтверждён hh.ru для '%s' в '%s'", title, company), nil
					}
				}
				time.Sleep(400 * time.Millisecond)
			}
			return "", fmt.Errorf("кнопка отправки нажата, но hh.ru не подтвердил результат; проверьте раздел «Мои отклики» перед повторным запуском")
		}
	}

	return "", fmt.Errorf("не найдена видимая кнопка отправки; отклик не отправлен")
}

func clickVisible(page playwright.Page, selectors []string) error {
	for _, selector := range selectors {
		locator := page.Locator(selector)
		count, _ := locator.Count()
		for i := 0; i < count; i++ {
			button := locator.Nth(i)
			visible, _ := button.IsVisible()
			if visible {
				if err := button.Click(); err == nil {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("не найдена видимая кнопка среди селекторов: %s", strings.Join(selectors, ", "))
}

func fillVisibleMessage(page playwright.Page, message string) error {
	selectors := []string{
		"textarea[data-qa='vacancy-response-popup-form-letter-input']",
		"textarea[name='message']",
		"textarea",
		"[contenteditable='true']",
	}
	for _, selector := range selectors {
		locator := page.Locator(selector)
		count, _ := locator.Count()
		for i := 0; i < count; i++ {
			field := locator.Nth(i)
			visible, _ := field.IsVisible()
			if visible && field.Fill(message) == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("не найдено видимое поле письма")
}

func waitForAnyVisible(page playwright.Page, selectors []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, selector := range selectors {
			locator := page.Locator(selector)
			count, _ := locator.Count()
			for i := 0; i < count; i++ {
				if visible, _ := locator.Nth(i).IsVisible(); visible {
					return nil
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("элемент подтверждения не появился")
}

func (a *AIOrchestrator) printJobAnalysis(job types.Job, evaluation *JobEvaluationResult) {

	var scoreColor string
	var scoreEmoji string

	switch {
	case evaluation.MatchScore >= 9:
		scoreColor = "🟢"
		scoreEmoji = "🔥"
	case evaluation.MatchScore >= 7:
		scoreColor = "🟡"
		scoreEmoji = "👍"
	case evaluation.MatchScore >= 5:
		scoreColor = "🟠"
		scoreEmoji = "🤔"
	default:
		scoreColor = "🔴"
		scoreEmoji = "❌"
	}

	output := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════════════════╗
║                              АНАЛИЗ ВАКАНСИИ                                ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  📝 Должность: %s
║  🏢 Компания:  %s
║  %s Оценка:    %d/10 %s
║  📊 Релевантность: %v
╠══════════════════════════════════════════════════════════════════════════════╣
║  🧠 АНАЛИЗ МОДЕЛИ:
║  %s
╠══════════════════════════════════════════════════════════════════════════════╣
║  ✅ Совпадающие навыки: %s
║  📋 Основные требования: %s
║  🤔 Рекомендация: %s
║  🔗 Ссылка: %s
╚══════════════════════════════════════════════════════════════════════════════╝`,
		truncate(job.Title, 60),
		truncate(job.Company, 50),
		scoreColor,
		evaluation.MatchScore,
		scoreEmoji,
		evaluation.IsRelevant,
		evaluation.Analysis,
		strings.Join(evaluation.SkillsMatch, ", "),
		strings.Join(evaluation.Requirements, ", "),
		getRecommendationText(evaluation.Recommendation),
		truncate(job.URL, 70))

	fmt.Println(output)
}

func (a *AIOrchestrator) evaluateJobForTaskWithAnalysis(job types.Job, userTask string) (*JobEvaluationResult, error) {

	prompt := fmt.Sprintf(`Проанализируй вакансию и дай развернутый анализ.

ЗАДАЧА ПОЛЬЗОВАТЕЛЯ: "%s"

ИНФОРМАЦИЯ О ВАКАНСИИ:
- Должность: %s
- Компания: %s
- Описание: %s

Информация из резюме кандидата:
- Позиция: %s
- Навыки: %s
- Опыт: %s

ПРОАНАЛИЗИРУЙ ПО КРИТЕРИЯМ:
1. Соответствие должности запросу пользователя
2. Соответствие требуемых навыков навыкам кандидата
3. Уровень опыта (junior/middle/senior)
4. Зарплатные ожидания (если указана зарплата)
5. Локация и формат работы (офис/удаленка)
6. Плюсы и минусы вакансии

ВЕРНИ ОТВЕТ В ФОРМАТЕ JSON:
{
    "is_relevant": true/false,
    "reason": "краткое обоснование релевантности",
    "match_score": число от 1 до 10,
    "recommendation": "apply/skip/review",
    "analysis": "развернутый анализ на 3-4 предложения",
    "skills_match": ["Python", "Django", "SQL"],
    "requirements": ["Опыт работы 2+ года", "Знание Python"]
}

Важно: Будь конкретен и объективен в анализе.`,
		userTask,
		job.Title,
		job.Company,
		truncate(job.Snippet, 500),
		a.resume.Title,
		strings.Join(a.resume.Skills, ", "),
		formatExperience(a.resume.Experience))

	response, err := a.llmClient.GenerateJSON(prompt, "Ты эксперт по анализу вакансий. Будь объективен, не придумывай факты и верни JSON-объект требуемой структуры.")
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации анализа: %w", err)
	}

	var result JobEvaluationResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w\nОтвет: %s", err, response)
	}

	return &result, nil
}

func (a *AIOrchestrator) evaluateAllJobsOnPage() (string, error) {
	log.Println("🧠 Начинаю анализ всех вакансий на странице...")

	jobs, err := a.extractJobsFromPage(10)
	if err != nil {
		return "", fmt.Errorf("не удалось извлечь вакансии: %w", err)
	}

	log.Printf("📊 Найдено %d вакансий для анализа", len(jobs))

	var evaluations []string
	relevantCount := 0

	for i, job := range jobs {
		log.Printf("🔍 Анализ вакансии %d/%d: %s", i+1, len(jobs), job.Title)

		evaluation, err := a.evaluateJobForTaskWithAnalysis(job, a.currentTask)
		if err != nil {
			log.Printf("⚠️ Ошибка оценки: %v", err)
			evaluations = append(evaluations, fmt.Sprintf("❌ %s: ошибка анализа", job.Title))
			continue
		}

		status := "⏭️"
		if evaluation.IsRelevant {
			status = "✅"
			relevantCount++
		}

		evaluations = append(evaluations, fmt.Sprintf("%s %s | %s | Оценка: %d/10 | %s",
			status, job.Title, job.Company, evaluation.MatchScore, evaluation.Reason))

		a.lastEvaluatedJobs = jobs

		time.Sleep(1 * time.Second)
	}

	result := fmt.Sprintf("\n📊 РЕЗУЛЬТАТЫ АНАЛИЗА\n%s\nПроанализировано: %d вакансий\nРелевантных: %d\n\n%s",
		strings.Repeat("=", 50),
		len(jobs),
		relevantCount,
		strings.Join(evaluations, "\n"))

	return result, nil
}

func getRecommendationText(rec string) string {
	switch strings.ToLower(rec) {
	case "apply":
		return "✅ ОТКЛИКНУТЬСЯ"
	case "skip":
		return "⏭️ ПРОПУСТИТЬ"
	case "review":
		return "🤔 ТРЕБУЕТ ДОП. АНАЛИЗА"
	default:
		return rec
	}
}

func (a *AIOrchestrator) generateCoverLetter(title, company, vacancyDescription string) (string, error) {
	profileSummary := cleanProfileText(a.resume.Summary)
	skills := cleanProfileSkills(a.resume.Skills)
	// The configured local model can produce fluent-sounding but invented
	// claims despite strict instructions. For an outgoing application message,
	// prefer a short deterministic draft built only from the saved profile and
	// the vacancy facts we extracted from hh.ru.
	letter := buildCoverLetterFallback(title, company, vacancyDescription, skills, profileSummary)
	log.Println("✉️ Сопроводительное составлено по фактам из профиля без неподтверждённых утверждений модели")
	return letter, nil
}

func buildCoverLetterFallback(title, company, description string, skills []string, summary string) string {
	opening := "Меня заинтересовала вакансия «" + strings.TrimSpace(title) + "»"
	if company = strings.TrimSpace(company); company != "" {
		opening += " в компании «" + company + "»"
	}
	parts := []string{"Здравствуйте! " + opening + "."}

	matchedSkills := make([]string, 0, len(skills))
	for _, skill := range skills {
		pattern := `(?i)(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(skill) + `($|[^\p{L}\p{N}])`
		if matched, _ := regexp.MatchString(pattern, description); matched {
			matchedSkills = append(matchedSkills, skill)
		}
	}
	if len(matchedSkills) > 0 {
		parts = append(parts, "В профиле указаны навыки "+strings.Join(matchedSkills, ", ")+", которые также требуются в вакансии.")
	} else if summary != "" && summary != "не указано" {
		candidateFacts := firstSentences(summary, 2)
		sentences := strings.SplitN(candidateFacts, ".", 2)
		first := strings.TrimSpace(sentences[0])
		if first != "" && !strings.HasPrefix(strings.ToLower(first), "я ") {
			first = "Я — " + lowerFirstRune(first)
		}
		candidateFacts = first
		if len(sentences) > 1 {
			candidateFacts += ". " + strings.TrimSpace(sentences[1])
		}
		parts = append(parts, strings.TrimSpace(candidateFacts)+".")
	}
	parts = append(parts, "Буду рад обсудить, насколько мой текущий опыт соответствует задачам вакансии.")
	return truncateRunes(strings.Join(parts, " "), 650)
}

func firstSentences(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 {
		return ""
	}
	var result []string
	for _, part := range regexp.MustCompile(`[^.!?]+[.!?]?`).FindAllString(value, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = append(result, part)
		if len(result) == limit {
			break
		}
	}
	return strings.Join(result, " ")
}

func lowerFirstRune(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return ""
	}
	runes[0] = []rune(strings.ToLower(string(runes[0])))[0]
	return string(runes)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func cleanProfileSkills(skills []string) []string {
	cleaned := make([]string, 0, len(skills))
	for _, skill := range skills {
		skill = strings.TrimSpace(skill)
		if skill == "" || strings.ContainsAny(skill, "[]") || containsTemplateInstruction(skill) {
			continue
		}
		cleaned = append(cleaned, skill)
	}
	return cleaned
}

func cleanProfileText(value string) string {
	parts := regexp.MustCompile(`(?m)([^.!?\n]+[.!?]?|[^.!?\n]+$)`).FindAllString(value, -1)
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.ContainsAny(part, "[]") || containsTemplateInstruction(part) {
			continue
		}
		cleaned = append(cleaned, part)
	}
	return strings.Join(cleaned, " ")
}

func containsTemplateInstruction(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{"оставь нужное", "оставьте нужное", "вставьте", "укажите здесь", "пример:"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func (a *AIOrchestrator) generateFinalReport() string {
	if len(a.actionHistory) == 0 {
		return "Нет данных для отчета"
	}

	var appliedJobs []string
	var analyzedCount int

	for _, action := range a.actionHistory {
		if action["action"] == "apply_to_relevant" || action["action"] == "apply_job" {
			if result, ok := action["result"].(string); ok {
				if strings.Contains(result, "отправлен") {
					appliedJobs = append(appliedJobs, result)
				}
			}
		}
	}

	analyzedCount = len(a.lastEvaluatedJobs)

	report := fmt.Sprintf(`
📋 ФИНАЛЬНЫЙ ОТЧЕТ ВЫПОЛНЕНИЯ ЗАДАЧИ
%s
Задача: %s
Выполнено шагов: %d
Проанализировано вакансий: %d
Отправлено откликов: %d
%s`,
		strings.Repeat("=", 60),
		a.currentTask,
		len(a.actionHistory),
		analyzedCount,
		len(appliedJobs),
		strings.Repeat("=", 60))

	if len(appliedJobs) > 0 {
		report += "\n\n✅ ОТПРАВЛЕННЫЕ ОТКЛИКИ:\n" + strings.Join(appliedJobs, "\n")
	}

	return report
}

func (a *AIOrchestrator) navigate(url string) (string, error) {
	log.Printf("🌐 Навигация на %s", url)
	_, err := a.page.Goto(url, playwright.PageGotoOptions{
		Timeout:   playwright.Float(30000),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		return "", fmt.Errorf("навигация не удалась: %w", err)
	}

	time.Sleep(2 * time.Second)

	if a.debug {
		a.takeScreenshot("navigate_" + sanitizeFilename(url))
	}

	return "Успешно перешли на страницу", nil
}

func (a *AIOrchestrator) searchJobs(query string) (string, error) {
	log.Printf("🔍 Поиск вакансий: %s", query)
	areaID := a.areaID
	if areaID == 0 {
		areaID = hhAreaForText(query)
	}
	searchURL := fmt.Sprintf("https://hh.ru/search/vacancy?text=%s&area=%d", url.QueryEscape(query), areaID)

	log.Printf("🌐 Перехожу на URL поиска: %s", searchURL)
	_, err := a.page.Goto(searchURL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(30000),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		return "", fmt.Errorf("ошибка перехода на страницу поиска: %w", err)
	}

	time.Sleep(3 * time.Second)

	currentURL := a.page.URL()
	log.Printf("📄 Текущий URL: %s", currentURL)

	parsedURL, parseErr := url.Parse(currentURL)
	if parseErr != nil || (parsedURL.Hostname() != "hh.ru" && !strings.HasSuffix(parsedURL.Hostname(), ".hh.ru")) {
		return "", fmt.Errorf("hh.ru перенаправил поиск на неожиданный адрес: %s", currentURL)
	}
	if strings.Contains(parsedURL.Path, "/account/login") {
		return "", fmt.Errorf("hh.ru перенаправил на вход; проверьте авторизацию и повторите поиск")
	}
	if parsedURL.Path != "/search/vacancy" && !strings.HasPrefix(parsedURL.Path, "/vacancies/") {
		return "", fmt.Errorf("после поиска открылась не страница вакансий: %s", currentURL)
	}
	if !strings.Contains(currentURL, "text=") {
		log.Println("ℹ️ hh.ru преобразовал поисковый URL в адрес страницы результатов")
	}

	return fmt.Sprintf("Поиск выполнен: %s", query), nil
}

func (a *AIOrchestrator) analyzePage() (string, error) {
	content, err := a.page.Content()
	if err != nil {
		return "", fmt.Errorf("не удалось получить содержимое страницы: %w", err)
	}

	if len(content) > 4000 {
		content = content[:4000]
	}

	promptText := fmt.Sprintf(`Проанализируй содержимое страницы HH.ru и определи:
1. Это страница с результатами поиска или отдельная вакансия?
2. Сколько вакансий на странице примерно?
3. Какая основная тематика вакансий?

Содержимое страницы: %s`, content)

	analysis, err := a.llmClient.Generate(promptText, "Ты анализируешь веб-страницы HH.ru. Будь кратким и точным.")
	if err != nil {
		return "", err
	}

	return analysis, nil
}

func (a *AIOrchestrator) checkLogin() (string, error) {
	loggedIn, err := isLoggedIn(a.page)
	if err != nil {
		return "", err
	}

	if loggedIn {
		return "✅ Пользователь авторизован", nil
	}

	return "⚠️ Требуется авторизация", nil
}

func (a *AIOrchestrator) handleCaptcha() (string, error) {
	log.Println("⚠️ Обнаружена CAPTCHA")
	return "CAPTCHA обнаружена - требуется ручное вмешательство", nil
}

func (a *AIOrchestrator) handleGenericAction(actionName string, params map[string]interface{}) (string, error) {
	log.Printf("🔄 Обрабатываю общее действие: %s", actionName)
	return fmt.Sprintf("Действие '%s' выполнено", actionName), nil
}

func loadResume(path string) types.Resume {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("⚠️ Не удалось загрузить профиль кандидата %q: %v", path, err)
		return types.Resume{}
	}
	var resume types.Resume
	if err := json.Unmarshal(data, &resume); err != nil {
		log.Printf("⚠️ Некорректный профиль кандидата %q: %v", path, err)
		return types.Resume{}
	}
	return resume
}

func formatExperience(items []types.Experience) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, strings.TrimSpace(strings.Join([]string{item.Position, item.Company, item.Period, strings.Join(item.Responsibilities, ", ")}, " ")))
	}
	return strings.Join(parts, "; ")
}

func (a *AIOrchestrator) GetActionHistory() []map[string]interface{} {
	return a.actionHistory
}

func (a *AIOrchestrator) takeScreenshot(name string) {
	screenshotPath := fmt.Sprintf("data/screenshots/%s_%d.png", name, time.Now().Unix())
	if _, err := a.page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(screenshotPath),
		FullPage: playwright.Bool(true),
	}); err == nil {
		log.Printf("📸 Скриншот сохранен: %s", screenshotPath)
	}
}

func sanitizeFilename(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_\-.]`)
	return re.ReplaceAllString(name, "_")
}

func (a *AIOrchestrator) searchViaInputField(query string) (string, error) {
	searchInput, err := findSearchInput(a.page)
	if err != nil {
		return "", err
	}

	if err := searchInput.Click(playwright.LocatorClickOptions{ClickCount: playwright.Int(3)}); err != nil {
		log.Printf("⚠️ Не удалось кликнуть: %v", err)
	}

	if err := searchInput.Fill(query); err != nil {
		return "", fmt.Errorf("ошибка ввода: %w", err)
	}

	if err := searchInput.Press("Enter"); err != nil {
		return "", fmt.Errorf("ошибка Enter: %w", err)
	}

	time.Sleep(3 * time.Second)
	return "Поиск выполнен через поле ввода", nil
}

func (a *AIOrchestrator) extractJobsFromPage(count int) ([]types.Job, error) {
	log.Printf("🔍 Извлекаю до %d вакансий со страницы...", count)
	var jobs []types.Job

	page := a.page

	time.Sleep(2 * time.Second)

	noResultsSelector := "text='По запросу ничего не найдено', text='Ничего не найдено'"
	if count, _ := page.Locator(noResultsSelector).Count(); count > 0 {
		return jobs, fmt.Errorf("вакансий не найдено по данному запросу")
	}

	vacancySelectors := []string{
		"[data-qa='serp-item__title']", // Основной селектор HH.ru
		"a[href*='/vacancy/']:visible",
	}

	for _, selector := range vacancySelectors {
		vacancyLocator := page.Locator(selector)

		err := vacancyLocator.First().WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateAttached,
			Timeout: playwright.Float(10000),
		})

		if err != nil {
			log.Printf("⚠️ Селектор %s не найден: %v", selector, err)
			continue
		}

		locatorCount, err := vacancyLocator.Count()
		if err != nil {
			log.Printf("⚠️ Ошибка подсчета элементов: %v", err)
			continue
		}

		if locatorCount > 0 {
			log.Printf("✅ Найдено %d элементов по селектору: %s", locatorCount, selector)
			limit := min(locatorCount, count)

			for i := 0; i < limit; i++ {
				item := vacancyLocator.Nth(i)

				visible, err := item.IsVisible()
				if err != nil || !visible {
					continue
				}

				href, err := item.GetAttribute("href")
				if err != nil || href == "" {
					continue
				}

				text, err := item.TextContent()
				if err != nil || text == "" {
					continue
				}

				title := cleanJobTitle(text)

				company := ""

				fullURL := normalizeVacancyURL(href)

				if !isRealVacancyURL(fullURL) {
					continue
				}

				snippet := ""

				job := types.Job{
					Title:   title,
					Company: company,
					URL:     fullURL,
					Snippet: snippet,
				}

				jobs = append(jobs, job)

				if a.debug {
					log.Printf("📝 Вакансия %d: %s | %s", i+1, truncate(job.Title, 40), truncate(job.URL, 40))
				}

				time.Sleep(100 * time.Millisecond)
			}

			if len(jobs) > 0 {
				log.Printf("✅ Успешно извлечено %d вакансий", len(jobs))
				return jobs, nil
			}
		}
	}

	log.Println("⚠️ Основные селекторы не сработали, использую fallback...")
	return a.extractJobsFallback(count)
}

func (a *AIOrchestrator) extractJobsFallback(count int) ([]types.Job, error) {
	var jobs []types.Job

	page := a.page

	links := page.Locator("a[href*='vacancy']:visible")

	linkCount, err := links.Count()
	if err != nil {
		return jobs, fmt.Errorf("не удалось найти ссылки: %w", err)
	}

	log.Printf("ℹ️ Найдено %d ссылок с 'vacancy'", linkCount)
	limit := min(linkCount, count)

	for i := 0; i < limit; i++ {
		link := links.Nth(i)

		href, err := link.GetAttribute("href")
		if err != nil || href == "" {
			continue
		}

		if !isRealVacancyURL(href) {
			continue
		}

		text, _ := link.TextContent()
		title := cleanJobTitle(text)

		job := types.Job{
			Title:   title,
			Company: "",
			URL:     normalizeVacancyURL(href),
			Snippet: "",
		}

		jobs = append(jobs, job)

		if a.debug {
			log.Printf("🔗 Вакансия %d: %s", i+1, truncate(job.Title, 30))
		}
	}

	if len(jobs) == 0 {
		return jobs, fmt.Errorf("не удалось извлечь ни одной вакансии")
	}

	return jobs, nil
}

func cleanJobTitle(title string) string {
	if title == "" {
		return ""
	}
	re := regexp.MustCompile(`^Сейчас (смотрят|смотрит) \d+ (человек|человека)`)
	title = re.ReplaceAllString(title, "")
	title = strings.ReplaceAll(title, "\n", " ")
	title = strings.ReplaceAll(title, "\t", " ")
	title = regexp.MustCompile(`\s+`).ReplaceAllString(title, " ")
	return strings.TrimSpace(title)
}
