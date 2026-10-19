package browser

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	agentprompt "github.com/mihnpro/Ai_agent_2/internal/prompt"
	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
)

type AIOrchestrator struct {
	applicationTarget  int
	vacancyLimit       int
	minimumScore       int
	searchGroups       [][]types.Job
	searchTerms        []string
	vacanciesAnalyzed  int
	letterExamplesPath string
	jobDetails         map[string]types.Job
	llmClient          *llm.OllamaClient
	page               playwright.Page
	context            playwright.BrowserContext
	pw                 *playwright.Playwright
	profileDir         string
	headless           bool
	onContextChanged   func(playwright.BrowserContext)
	debug              bool
	currentTask        string
	actionHistory      []map[string]interface{}
	resume             types.Resume
	autoApply          bool
	areaID             int
	lastEvaluatedJobs  []types.Job
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

// Small local models sometimes return a string, an object, or a list of
// objects for explanatory fields. Keep the decision fields strict enough to
// validate below, but normalize these display-only values instead of failing
// the entire vacancy evaluation.
func (r *JobEvaluationResult) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	readString := func(key string) string {
		var value string
		_ = json.Unmarshal(fields[key], &value)
		return strings.TrimSpace(value)
	}
	var relevant bool
	_ = json.Unmarshal(fields["is_relevant"], &relevant)
	var score int
	if err := json.Unmarshal(fields["match_score"], &score); err != nil {
		var value string
		if json.Unmarshal(fields["match_score"], &value) == nil {
			score, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	readList := func(key string) []string {
		raw := fields[key]
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		var one string
		if json.Unmarshal(raw, &one) == nil {
			if strings.TrimSpace(one) == "" {
				return nil
			}
			return []string{strings.TrimSpace(one)}
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return []string{compactJSON(raw)}
		}
		items := make([]string, 0, len(values))
		for _, item := range values {
			var text string
			if json.Unmarshal(item, &text) == nil {
				if strings.TrimSpace(text) != "" {
					items = append(items, strings.TrimSpace(text))
				}
				continue
			}
			if normalized := compactJSON(item); normalized != "" {
				items = append(items, normalized)
			}
		}
		return items
	}
	r.IsRelevant = relevant
	r.Reason = readString("reason")
	r.MatchScore = score
	r.Recommendation = strings.ToLower(readString("recommendation"))
	r.Analysis = readString("analysis")
	r.SkillsMatch = readList("skills_match")
	r.Requirements = readList("requirements")
	return nil
}

func compactJSON(raw json.RawMessage) string {
	var value interface{}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	switch item := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"name", "title", "text", "requirement", "skill", "description"} {
			if text, ok := item[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func CreateLLMOrchestrator(modelURL, modelName, resumePath string, page playwright.Page, ctx playwright.BrowserContext, debug bool) *AIOrchestrator {
	client := llm.NewOllamaClient(modelURL, modelName)
	resume := loadResume(resumePath)

	return &AIOrchestrator{
		llmClient:          client,
		page:               page,
		context:            ctx,
		debug:              debug,
		actionHistory:      []map[string]interface{}{},
		resume:             resume,
		letterExamplesPath: filepath.Join(filepath.Dir(resumePath), "cover-letter-examples.local.json"),
		jobDetails:         map[string]types.Job{},
		minimumScore:       types.DefaultMinimumScore,
	}
}

func (a *AIOrchestrator) ExecuteTask(task string, autoApply bool) (string, error) {
	log.Printf("🤖 AI Agent processing task: %s (autoApply: %v)", task, autoApply)
	if autoApply && (strings.TrimSpace(a.resume.Name) == "" || strings.TrimSpace(a.resume.Title) == "" || len(a.resume.Skills) == 0) {
		return "", fmt.Errorf("автоотклики требуют заполненного профиля кандидата: укажите name, title и skills в файле резюме")
	}
	a.autoApply = autoApply
	a.currentTask = task
	if strings.TrimSpace(a.resume.Summary) == "" && len(a.resume.Skills) == 0 && len(a.resume.Experience) == 0 {
		log.Println("⚠️ Профиль пуст: заполните и сохраните его в панели для оценки соответствия и персональных писем")
	}

	plan, err := a.createPlan(task, autoApply)
	if err != nil {
		return "", fmt.Errorf("plan creation failed: %w", err)
	}

	log.Printf("📋 AI создал план из %d шагов", len(plan))

	result, err := a.executePlan(plan)
	if err != nil {
		return "", fmt.Errorf("plan execution failed: %w", err)
	}

	if autoApply {
		return result, nil
	}
	finalReport := a.generateFinalReport()

	return finalReport + "\n\n" + result, nil
}

func (a *AIOrchestrator) createPlan(task string, autoApply bool) ([]AIAction, error) {
	if strings.TrimSpace(task) == "" {
		return nil, fmt.Errorf("укажите профессию или задачу поиска")
	}
	queries := a.searchQueries(task)
	if len(queries) == 0 {
		return nil, fmt.Errorf("не удалось определить профессию: уточните задачу")
	}
	if autoApply {
		return []AIAction{{Action: "apply_until_target", Parameters: map[string]interface{}{"queries": queries}, Reason: "Искать подходящие вакансии до достижения количества откликов"}}, nil
	}
	count := a.vacancyLimit
	if count < 1 {
		count = types.DefaultVacancyLimit
	}
	if match := regexp.MustCompile(`(?i)(\d{1,2})\s+(?:подходящ\p{L}*\s+)?ваканси\p{L}*`).FindStringSubmatch(task); len(match) == 2 {
		if parsed, err := strconv.Atoi(match[1]); err == nil && parsed > 0 {
			count = min(parsed, count)
		}
	}
	a.vacancyLimit = count
	a.lastEvaluatedJobs = nil
	a.searchGroups = nil
	a.searchTerms = append([]string(nil), queries...)
	a.vacanciesAnalyzed = 0
	searchBatch := min(max(count*2, 20), types.MaxVacancyLimit)
	plan := make([]AIAction, 0, 2*len(queries)+1)
	for _, query := range queries {
		plan = append(plan,
			AIAction{Action: "search", Parameters: map[string]interface{}{"query": query}, Reason: "Поиск: " + query},
			AIAction{Action: "extract_jobs", Parameters: map[string]interface{}{"count": float64(searchBatch)}, Reason: "Собрать вакансии без повторов"},
		)
	}
	plan = append(plan, AIAction{Action: "evaluate_jobs", Parameters: map[string]interface{}{"count": float64(count)}, Reason: "Оценить вакансии по описанию и профилю"})

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
			if action.Action == "extract_jobs" {
				continue
			}
			// Do not evaluate a stale results page after a failed search.
			return strings.Join(results, "\n"), err
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
		a.searchGroups = append(a.searchGroups, jobs)
		a.lastEvaluatedJobs = mergeJobs(a.lastEvaluatedJobs, jobs)
		return fmt.Sprintf("Извлечено %d вакансий, всего уникальных: %d", len(jobs), len(a.lastEvaluatedJobs)), nil

	case "evaluate_jobs":
		candidateCount := 0
		for _, group := range a.searchGroups {
			candidateCount += len(group)
		}
		a.lastEvaluatedJobs = selectSearchResults(a.searchGroups, candidateCount)
		return a.evaluateAllJobsOnPage()

	case "apply_until_target":
		queries, ok := action.Parameters["queries"].([]string)
		if !ok {
			return "", fmt.Errorf("не заданы поисковые запросы")
		}
		return a.applyUntilTarget(queries)
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
		log.Printf("🤔 Анализирую вакансию %d/%d: %s | URL: %s",
			i+1, len(a.lastEvaluatedJobs), job.Title, job.URL)

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

		shouldApply := evaluation.IsRelevant && evaluation.MatchScore >= 7 && evaluation.Recommendation == "apply"

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
		if captchaPresent(page) {
			if captchaErr := a.waitForCaptcha(page); captchaErr != nil {
				return "", captchaErr
			}
			page = a.page
		} else {
			return "", fmt.Errorf("не удалось перейти на вакансию: %w", err)
		}
	}
	if err := a.handleCaptchaIfPresent(page); err != nil {
		return "", err
	}
	if page.IsClosed() {
		page = a.page
	}

	time.Sleep(2 * time.Second)

	already := page.Locator("button:has-text('Вы откликнулись'), a:has-text('Вы откликнулись')")
	if count, _ := already.Count(); count > 0 {
		if visible, _ := already.First().IsVisible(); visible {
			return "", errAlreadyApplied
		}
	}
	detailed, err := readJobDetails(page, job)
	if err != nil {
		return "", err
	}
	if a.jobDetails == nil {
		a.jobDetails = map[string]types.Job{}
	}
	a.jobDetails[job.URL] = detailed
	title, company := detailed.Title, detailed.Company
	coverLetter, err := a.generateCoverLetter(title, company, detailed)
	if err != nil {
		return "", fmt.Errorf("письмо не подготовлено, отклик отменён: %w", err)
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
			"a:has-text('Перейти в чат')",
			"button:has-text('Перейти в чат')",
			"text=Резюме доставлено",
		}, 8*time.Second); err != nil {
			return "", fmt.Errorf("hh.ru не подтвердил отклик без письма; проверьте «Мои отклики» перед повтором: %w", err)
		}

		// Prefer the direct chat action shown after an application. Some HH
		// response dialogs do not show it, so fall back to the matching card in
		// the applicant's response list.
		chatSelectors := []string{
			"a:has-text('Перейти в чат')",
			"button:has-text('Перейти в чат')",
			"text=Перейти в чат",
		}
		addLetterSelectors := []string{
			"button:has-text('Добавить сопроводительное')",
			"text=Добавить сопроводительное",
		}
		chatErr := clickVisible(page, chatSelectors)
		if chatErr == nil {
			log.Println("💬 Перехожу в чат по этой вакансии, чтобы добавить сопроводительное")
		}
		if err := waitForAnyVisible(page, addLetterSelectors, 4*time.Second); err != nil {
			log.Println("ℹ️ В окне отклика кнопки письма нет; открываю карточку этой вакансии в откликах")
			if openErr := openResponseCard(page, job.URL, title, company); openErr != nil {
				return "", fmt.Errorf("отклик отправлен без письма; не удалось открыть его карточку: %w", openErr)
			}
		}

		if err := clickVisible(page, addLetterSelectors); err != nil {
			return "", fmt.Errorf("отклик отправлен без письма; в чате не найдена кнопка «Добавить сопроводительное»: %w", err)
		}
		if err := waitForAnyVisible(page, []string{
			"textarea[data-qa='vacancy-response-popup-form-letter-input']",
			"textarea[name='message']",
			"textarea[placeholder*='сопровод']",
			"textarea[placeholder*='Сообщение']",
			"input[placeholder*='Сообщение']",
			"[contenteditable='true']",
		}, 5*time.Second); err != nil {
			return "", fmt.Errorf("отклик отправлен без письма; после нажатия кнопки поле ввода не появилось: %w", err)
		}
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

func openResponseCard(page playwright.Page, vacancyURL, title, company string) error {
	parsed, err := url.Parse(vacancyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("не удалось определить адрес откликов для вакансии")
	}
	responsesURL := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/applicant/vacancy_response"}).String()
	log.Println("🗂️ Открываю карточку отклика для добавления сопроводительного")
	if _, err := page.Goto(responsesURL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(30000),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return fmt.Errorf("не открылась страница «Мои отклики»: %w", err)
	}

	titles := page.GetByText(title, playwright.PageGetByTextOptions{Exact: playwright.Bool(true)})
	count, err := titles.Count()
	if err != nil || count == 0 {
		return fmt.Errorf("в списке откликов не найдена вакансия «%s»", title)
	}
	var match playwright.Locator
	var firstVisible playwright.Locator
	for i := 0; i < count; i++ {
		candidate := titles.Nth(i)
		visible, _ := candidate.IsVisible()
		if !visible {
			continue
		}
		if firstVisible == nil {
			firstVisible = candidate
		}
		if strings.TrimSpace(company) == "" {
			match = candidate
			break
		}
		containsCompany, evalErr := candidate.Evaluate(`(element, expected) => {
			let node = element;
			for (let level = 0; node && level < 10; level += 1, node = node.parentElement) {
				const text = node.innerText || "";
				if (text.includes(expected.title) && text.toLowerCase().includes(expected.company.toLowerCase())) return true;
			}
			return false;
		}`, map[string]string{"title": title, "company": company})
		if evalErr == nil && containsCompany == true {
			match = candidate
			cardChat := candidate.Locator("xpath=ancestor::*[.//a[contains(normalize-space(.), 'Перейти в чат')] or .//button[contains(normalize-space(.), 'Перейти в чат')]][1]")
			if chatCount, _ := cardChat.Count(); chatCount > 0 {
				if err := clickFirstVisible(cardChat.Locator("a:has-text('Перейти в чат'), button:has-text('Перейти в чат')")); err == nil {
					return waitForAnyVisible(page, []string{"button:has-text('Добавить сопроводительное')", "text=Добавить сопроводительное"}, 10*time.Second)
				}
			}
			break
		}
	}
	if match == nil {
		match = firstVisible
	}
	if match == nil {
		return fmt.Errorf("карточка вакансии «%s» не отображается в откликах", title)
	}
	if err := match.Click(); err != nil {
		return fmt.Errorf("не удалось открыть карточку вакансии «%s»: %w", title, err)
	}

	addLetterSelectors := []string{"button:has-text('Добавить сопроводительное')", "text=Добавить сопроводительное"}
	if err := waitForAnyVisible(page, append(addLetterSelectors, "a:has-text('Перейти в чат')", "button:has-text('Перейти в чат')"), 8*time.Second); err != nil {
		return fmt.Errorf("карточка отклика открылась, но действия письма или чата не появились: %w", err)
	}
	if clickVisible(page, addLetterSelectors) == nil {
		return nil
	}
	if err := clickVisible(page, []string{"a:has-text('Перейти в чат')", "button:has-text('Перейти в чат')"}); err != nil {
		return fmt.Errorf("в карточке не найдена ссылка на чат: %w", err)
	}
	return waitForAnyVisible(page, addLetterSelectors, 10*time.Second)
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

func clickFirstVisible(locator playwright.Locator) error {
	count, _ := locator.Count()
	for i := 0; i < count; i++ {
		candidate := locator.Nth(i)
		visible, _ := candidate.IsVisible()
		if visible {
			if err := candidate.Click(); err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("не найдено видимое действие")
}

func fillVisibleMessage(page playwright.Page, message string) error {
	selectors := []string{
		"textarea[data-qa='vacancy-response-popup-form-letter-input']",
		"textarea[name='message']",
		"textarea[placeholder*='сопровод']",
		"textarea[placeholder*='Сообщение']",
		"input[placeholder*='Сообщение']",
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
	detailed, err := a.loadJobDetails(job)
	if err != nil {
		return nil, err
	}
	job = detailed

	profile := a.resume
	profile.ContactInfo = types.Contact{}
	profile.Summary = truncateRunes(profile.Summary, 900)
	if len(profile.Skills) > 30 {
		profile.Skills = profile.Skills[:30]
	}
	if len(profile.Experience) > 5 {
		profile.Experience = profile.Experience[:5]
	}
	if len(profile.Projects) > 5 {
		profile.Projects = profile.Projects[:5]
	}
	if len(profile.Achievements) > 5 {
		profile.Achievements = profile.Achievements[:5]
	}
	job.Snippet = truncateRunes(job.Snippet, 3000)
	userTask = truncateRunes(userTask, 500)
	data, err := json.Marshal(struct {
		Task    string       `json:"task"`
		Resume  types.Resume `json:"resume"`
		Vacancy types.Job    `json:"vacancy"`
	}{userTask, profile, job})
	if err != nil {
		return nil, err
	}
	response, err := a.llmClient.GenerateJSONWithBudget(string(data), agentprompt.EvaluationSystem, 320, 90*time.Second)

	if err != nil {
		return nil, fmt.Errorf("ошибка генерации анализа: %w", err)
	}

	var result JobEvaluationResult
	if err := json.Unmarshal([]byte(extractJSONObject(response)), &result); err != nil {
		const retrySystem = `Верни ровно один JSON-объект без Markdown и пояснений. Строго соблюдай типы: is_relevant boolean, reason string, match_score integer 1..10, recommendation string (apply/skip/review), analysis string, skills_match string[], requirements string[]. Не выдумывай факты; если данных мало, recommendation=review.`
		retry, retryErr := a.llmClient.GenerateJSONWithBudget(string(data), retrySystem, 180, 45*time.Second)
		if retryErr != nil {
			return nil, fmt.Errorf("модель вернула некорректный JSON (%v); повторный запрос не удался: %w", err, retryErr)
		}
		response = retry
		if retryParseErr := json.Unmarshal([]byte(extractJSONObject(response)), &result); retryParseErr != nil {
			return nil, fmt.Errorf("ошибка парсинга JSON после повтора: %w; ответ: %s", retryParseErr, truncateRunes(response, 240))
		}
	}

	if result.MatchScore < 1 || result.MatchScore > 10 {
		return nil, fmt.Errorf("некорректная оценка модели")
	}
	if result.Recommendation != "apply" && result.Recommendation != "skip" && result.Recommendation != "review" {
		return nil, fmt.Errorf("некорректная рекомендация модели")
	}
	minimumScore := a.minimumScore
	if minimumScore < 0 || minimumScore > types.MaxMatchScore {
		minimumScore = types.DefaultMinimumScore
	}
	if result.MatchScore < minimumScore {
		result.IsRelevant = false
		result.Recommendation = "skip"
		thresholdReason := fmt.Sprintf("Оценка %d/10 ниже установленного порога %d/10.", result.MatchScore, minimumScore)
		if result.Reason != "" {
			result.Reason += " "
		}
		result.Reason += thresholdReason
	}
	return &result, nil
}

func (a *AIOrchestrator) evaluateAllJobsOnPage() (string, error) {
	target := a.vacancyLimit
	if target < 1 {
		target = types.DefaultVacancyLimit
	}
	minimumScore := a.minimumScore
	if minimumScore < 0 || minimumScore > types.MaxMatchScore {
		minimumScore = types.DefaultMinimumScore
	}

	candidates := append([]types.Job(nil), a.lastEvaluatedJobs...)
	a.lastEvaluatedJobs = nil
	if len(candidates) == 0 && len(a.searchTerms) == 0 {
		return "", fmt.Errorf("нет вакансий для оценки")
	}
	log.Printf("🧠 Ищу %d вакансий с оценкой соответствия от %d/10", target, minimumScore)

	scanBudget := min(max(target*10, 100), 1000)
	batchSize := min(max(target, 20), types.MaxVacancyLimit)
	seen := make(map[string]struct{}, scanBudget)
	for _, job := range candidates {
		seen[job.URL] = struct{}{}
	}
	pageNumbers := make([]int, len(a.searchTerms))
	emptyPages := make([]int, len(a.searchTerms))
	for i := range pageNumbers {
		pageNumbers[i] = 1 // page 0 for each query was collected by the plan.
	}
	nextQuery := 0
	loadNextPage := func() bool {
		if len(a.searchTerms) == 0 {
			return false
		}
		for tried := 0; tried < len(a.searchTerms); tried++ {
			queryIndex := nextQuery % len(a.searchTerms)
			nextQuery++
			if emptyPages[queryIndex] >= 2 || pageNumbers[queryIndex] >= 25 {
				continue
			}
			pageNumber := pageNumbers[queryIndex]
			pageNumbers[queryIndex]++
			if _, err := a.searchJobsPage(a.searchTerms[queryIndex], pageNumber); err != nil {
				log.Printf("⚠️ Не удалось открыть страницу %d поиска «%s»: %v", pageNumber+1, a.searchTerms[queryIndex], err)
				emptyPages[queryIndex]++
				continue
			}
			jobs, err := a.extractJobsFromPage(batchSize)
			if err != nil {
				emptyPages[queryIndex]++
				continue
			}
			fresh := make([]types.Job, 0, len(jobs))
			for _, job := range jobs {
				if job.URL == "" {
					continue
				}
				if _, exists := seen[job.URL]; exists {
					continue
				}
				seen[job.URL] = struct{}{}
				fresh = append(fresh, job)
			}
			if len(fresh) == 0 {
				emptyPages[queryIndex]++
				continue
			}
			emptyPages[queryIndex] = 0
			candidates = append(candidates, fresh...)
			log.Printf("📄 Добавил %d новых вакансий со страницы %d поиска «%s»", len(fresh), pageNumber+1, a.searchTerms[queryIndex])
			return true
		}
		return false
	}

	var evaluations []string
	qualifiedCount := 0
	analyzedCount := 0
	nextCandidate := 0
	for qualifiedCount < target && analyzedCount < scanBudget {
		if nextCandidate >= len(candidates) && !loadNextPage() {
			break
		}
		if nextCandidate >= len(candidates) {
			continue
		}
		job := candidates[nextCandidate]
		nextCandidate++
		if !isRealVacancyURL(job.URL) {
			continue
		}
		analyzedCount++
		cardIndex := qualifiedCount + 1
		log.Printf("🔍 Анализ вакансии %d/%d: %s | URL: %s", cardIndex, target, job.Title, job.URL)
		log.Println("🧠 Запрашиваю анализ у AI модели...")
		evaluation, err := a.evaluateJobForTaskWithAnalysis(job, a.currentTask)
		if err != nil {
			log.Printf("⚠️ Ошибка оценки: %v", err)
			continue
		}
		if detailed, ok := a.jobDetails[job.URL]; ok {
			job = detailed
		}
		a.lastEvaluatedJobs = append(a.lastEvaluatedJobs, job)
		reason := strings.Join(strings.Fields(evaluation.Reason), " ")
		log.Printf("✅ Оценка вакансии завершена: оценка: %d/10; рекомендация: %s; причина: %s", evaluation.MatchScore, evaluation.Recommendation, truncateRunes(reason, 240))
		if evaluation.MatchScore < minimumScore {
			log.Printf("⏭️ Решение: пропустить (оценка: %d/10 ниже порога %d/10)", evaluation.MatchScore, minimumScore)
			time.Sleep(time.Second)
			continue
		}

		qualifiedCount++
		status := "✅"
		if !evaluation.IsRelevant {
			status = "⏭️"
		}
		evaluations = append(evaluations, fmt.Sprintf("%s %s | %s | Оценка: %d/10 | %s",
			status, job.Title, job.Company, evaluation.MatchScore, evaluation.Reason))
		if evaluation.IsRelevant {
			letter, err := a.generateCoverLetter(job.Title, job.Company, job)
			if err != nil {
				log.Printf("⚠️ Черновик письма не подготовлен: %v", err)
			} else {
				log.Printf("✉️ Черновик для «%s» (отдельно не отправлен):\n%s", job.Title, letter)
			}
		}
		time.Sleep(time.Second)
	}
	a.vacanciesAnalyzed = analyzedCount
	if analyzedCount == 0 {
		return "", fmt.Errorf("не удалось оценить ни одной вакансии; проверьте модель и доступность описаний")
	}
	result := fmt.Sprintf("\n📊 РЕЗУЛЬТАТЫ ПОИСКА\n%s\nПроверено: %d вакансий\nПодтверждено от %d/10: %d из %d\n\n%s",
		strings.Repeat("=", 50), analyzedCount, minimumScore, qualifiedCount, target, strings.Join(evaluations, "\n"))
	if qualifiedCount < target {
		result += fmt.Sprintf("\nПодходящих вакансий найдено меньше цели: просмотрено до %d вакансий и страниц поиска.", analyzedCount)
	}
	return result, nil
}

func extractJSONObject(response string) string {
	response = strings.TrimSpace(response)
	start := strings.IndexByte(response, '{')
	if start < 0 {
		return response
	}
	depth := 0
	inString, escaped := false, false
	for i := start; i < len(response); i++ {
		char := response[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		if char == '"' {
			inString = true
			continue
		}
		if char == '{' {
			depth++
		}
		if char == '}' {
			depth--
			if depth == 0 {
				return response[start : i+1]
			}
		}
	}
	return response[start:]
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

	analyzedCount = a.vacanciesAnalyzed

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
		if captchaPresent(a.page) {
			if captchaErr := a.waitForCaptcha(a.page); captchaErr != nil {
				return "", captchaErr
			}
		} else {
			return "", fmt.Errorf("навигация не удалась: %w", err)
		}
	}
	if err := a.handleCaptchaIfPresent(a.page); err != nil {
		return "", err
	}

	time.Sleep(2 * time.Second)

	if a.debug {
		a.takeScreenshot("navigate_" + sanitizeFilename(url))
	}

	return "Успешно перешли на страницу", nil
}

func (a *AIOrchestrator) searchJobs(query string) (string, error) { return a.searchJobsPage(query, 0) }

func (a *AIOrchestrator) searchJobsPage(query string, pageNumber int) (string, error) {
	log.Printf("🔍 Поиск вакансий: %s", query)
	areaID := a.areaID
	if areaID == 0 {
		areaID = hhAreaForText(a.currentTask)
	}
	searchURL := fmt.Sprintf("https://hh.ru/search/vacancy?text=%s&area=%d&page=%d", url.QueryEscape(query), areaID, pageNumber)

	log.Printf("🌐 Перехожу на URL поиска: %s", searchURL)
	_, err := a.page.Goto(searchURL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(30000),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		if captchaPresent(a.page) {
			if captchaErr := a.waitForCaptcha(a.page); captchaErr != nil {
				return "", captchaErr
			}
		} else {
			return "", fmt.Errorf("ошибка перехода на страницу поиска: %w", err)
		}
	}
	if err := a.handleCaptchaIfPresent(a.page); err != nil {
		return "", err
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
	if !captchaPresent(a.page) {
		return "Капча не обнаружена", nil
	}
	if err := a.waitForCaptcha(a.page); err != nil {
		return "", err
	}
	return "Проверка пройдена, продолжаю работу", nil
}

func (a *AIOrchestrator) handleCaptchaIfPresent(page playwright.Page) error {
	if !captchaPresent(page) {
		return nil
	}
	return a.waitForCaptcha(page)
}

func (a *AIOrchestrator) waitForCaptcha(challengePage playwright.Page) error {
	if challengePage == nil {
		return fmt.Errorf("не удалось открыть страницу проверки")
	}
	challengeURL := challengePage.URL()
	log.Println("CAPTCHA_REQUIRED: Пройдите капчу в окне браузера")

	if a.headless {
		// A headless window cannot be brought to the screen. Reopen the same
		// persistent profile in headed mode at the challenge URL.
		if err := a.context.Close(); err != nil {
			return fmt.Errorf("не удалось открыть окно для прохождения капчи: %w", err)
		}
		visibleContext, err := createBrowserContext(a.pw, a.profileDir, false, a.debug)
		if err != nil {
			return fmt.Errorf("не удалось открыть окно для прохождения капчи: %w", err)
		}
		visiblePage, err := visibleContext.NewPage()
		if err != nil {
			_ = visibleContext.Close()
			return fmt.Errorf("не удалось открыть страницу капчи: %w", err)
		}
		a.context = visibleContext
		a.page = visiblePage
		a.headless = false
		if a.onContextChanged != nil {
			a.onContextChanged(visibleContext)
		}
		if challengeURL != "" {
			if _, err := visiblePage.Goto(challengeURL, playwright.PageGotoOptions{
				Timeout: playwright.Float(30000), WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			}); err != nil {
				if !captchaPresent(visiblePage) {
					return fmt.Errorf("не удалось загрузить страницу капчи: %w", err)
				}
			}
		}
	} else {
		_ = challengePage.BringToFront()
	}

	for {
		if a.page.IsClosed() {
			return fmt.Errorf("окно браузера закрыто до прохождения капчи")
		}
		if !captchaPresent(a.page) {
			log.Println("CAPTCHA_RESOLVED: Проверка пройдена, продолжаю поиск")
			return nil
		}
		time.Sleep(1500 * time.Millisecond)
	}
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

	noResultsSelector := "text=/ничего не найдено|вакансий не найдено|нет подходящих вакансий/i"
	if count, _ := page.Locator(noResultsSelector).Count(); count > 0 {
		return jobs, errNoVacancies
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
		candidateFacts := strings.TrimSpace(firstSentences(summary, 2))
		if candidateFacts != "" {
			if !strings.ContainsAny(candidateFacts[len(candidateFacts)-1:], ".!?") {
				candidateFacts += "."
			}
			parts = append(parts, candidateFacts)
		}
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
