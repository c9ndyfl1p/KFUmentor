package browser

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
)

type Config = types.Config
type Job = types.Job

type LLMOrchestratorInterface interface {
	ExecuteTask(task string, autoApply bool) (string, error)
	GetActionHistory() []map[string]interface{}
}

func humanLikeDelay() {
	delays := []time.Duration{
		800 * time.Millisecond,
		1200 * time.Millisecond,
		1500 * time.Millisecond,
		1800 * time.Millisecond,
		2100 * time.Millisecond,
		2500 * time.Millisecond,
	}
	delay := delays[rand.Intn(len(delays))]
	time.Sleep(delay)
}

func createBrowserContext(pw *playwright.Playwright, profileDir string, headless bool, debug bool) (playwright.BrowserContext, error) {
	viewport := playwright.Size{Width: 1440, Height: 900}
	if profileDir != "" {
		absPath, err := filepath.Abs(profileDir)
		if err != nil {
			return nil, fmt.Errorf("could not determine absolute path for profile dir: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("could not create profile dir %s: %w", absPath, err)
		}
		log.Printf("Using persistent browser profile: %s", absPath)
		ctx, err := pw.Chromium.LaunchPersistentContext(absPath, playwright.BrowserTypeLaunchPersistentContextOptions{
			Headless:          playwright.Bool(headless),
			Viewport:          &viewport,
			AcceptDownloads:   playwright.Bool(true),
			TimezoneId:        playwright.String("Europe/Moscow"),
			JavaScriptEnabled: playwright.Bool(true),
			Locale:            playwright.String("ru-RU"),
			ColorScheme:       playwright.ColorSchemeLight,
		})
		if err == nil {
			return ctx, nil
		}
		if !debug {
			return nil, fmt.Errorf("could not launch persistent browser profile: %w", err)
		}
		log.Printf("Persistent browser profile unavailable, using temporary session: %v", err)
	}
	return createRegularContext(pw, headless)
}

func createRegularContext(pw *playwright.Playwright, headless bool) (playwright.BrowserContext, error) {
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
	})
	if err != nil {
		return nil, fmt.Errorf("could not launch browser: %w", err)
	}
	viewport := playwright.Size{Width: 1440, Height: 900}
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport:          &viewport,
		AcceptDownloads:   playwright.Bool(true),
		TimezoneId:        playwright.String("Europe/Moscow"),
		JavaScriptEnabled: playwright.Bool(true),
		Locale:            playwright.String("ru-RU"),
		ColorScheme:       playwright.ColorSchemeLight,
	})
	if err != nil {
		_ = browser.Close()
		return nil, fmt.Errorf("could not create browser context: %w", err)
	}
	return ctx, nil
}

// OpenLogin opens hh.ru in the persistent local profile and leaves the page
// under the user's control. It never resumes a search or submits applications.
func OpenLogin(cfg Config) error {
	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("could not start playwright: %w", err)
	}
	defer pw.Stop()

	ctx, err := createBrowserContext(pw, cfg.ProfileDir, false, cfg.Debug)
	if err != nil {
		return fmt.Errorf("could not open browser profile: %w", err)
	}
	defer func() { _ = ctx.Close() }()

	page, err := ctx.NewPage()
	if err != nil {
		return fmt.Errorf("could not create login page: %w", err)
	}
	if err := navigateWithRetry(page, "https://hh.ru/account/login", 30_000); err != nil {
		return fmt.Errorf("could not open hh.ru login page: %w", err)
	}
	if err := page.BringToFront(); err != nil {
		log.Printf("Could not bring hh.ru login page to front: %v", err)
	}

	fmt.Println("Окно hh.ru открыто. Войдите в аккаунт вручную; агент не продолжит поиск. Закройте окно после входа.")
	for !page.IsClosed() {
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func RunTask(task string, cfg Config) error {
	if os.Getenv("KFUMENTOR_QUEUE_ONLY") == "1" {
		cfg.UseAI = true
	}
	if cfg.AutoApply && !cfg.UseAI {
		return fmt.Errorf("для отправки заданного количества откликов включите UseAI в конфигурации")
	}
	log.Printf("Starting task: %s (AI mode: %v, Debug: %v)\n", task, cfg.UseAI, cfg.Debug)

	rand.Seed(time.Now().UnixNano())

	pw, err := playwright.Run()
	if err != nil {
		if strings.Contains(err.Error(), "please install the driver") || strings.Contains(strings.ToLower(err.Error()), "driver") {
			log.Println("Playwright driver not found — attempting automatic install...")
			if ierr := playwright.Install(); ierr != nil {
				return fmt.Errorf("playwright install failed: %w (original: %v)", ierr, err)
			}
			pw, err = playwright.Run()
		}
		if err != nil {
			return fmt.Errorf("could not start playwright: %w", err)
		}
	}
	defer func() {
		if err := pw.Stop(); err != nil {
			log.Printf("playwright stop error: %v", err)
		}
	}()

	ctx, err := createBrowserContext(pw, cfg.ProfileDir, cfg.Headless, cfg.Debug)
	if err != nil {
		return fmt.Errorf("could not create browser context: %w", err)
	}
	defer func() { _ = ctx.Close() }()

	page, err := ctx.NewPage()
	if err != nil {
		return fmt.Errorf("could not create page: %w", err)
	}

	if cfg.Debug {
		defer func() {
			if _, err := page.Screenshot(playwright.PageScreenshotOptions{
				Path:     playwright.String("data/screenshots/final_state.png"),
				FullPage: playwright.Bool(true),
			}); err == nil {
				log.Printf("📸 Скриншот сохранен: data/screenshots/final_state.png")
			}
		}()
	}

	if cfg.ProfileDir != "" {
		loggedIn, err := verifyAndHandleLogin(page, cfg.Headless, cfg.ProfileDir, cfg.AutoApply || os.Getenv("KFUMENTOR_QUEUE_ONLY") == "1")
		if err != nil {
			return err
		}

		if !loggedIn && (cfg.AutoApply || os.Getenv("KFUMENTOR_QUEUE_ONLY") == "1") {
			return fmt.Errorf("для автооткликов требуется войти в аккаунт. Используйте --headless=false для интерактивного входа")
		}
	}

	humanLikeDelay()

	if cfg.UseAI {
		log.Println("🔮 Использую AI-агента для выполнения задачи")

		orchestrator := CreateLLMOrchestrator(cfg.ModelURL, cfg.ModelName, cfg.ResumePath, page, ctx, cfg.Debug)
		orchestrator.pw = pw
		orchestrator.profileDir = cfg.ProfileDir
		orchestrator.headless = cfg.Headless
		orchestrator.onContextChanged = func(updated playwright.BrowserContext) {
			ctx = updated
			page = orchestrator.page
		}
		orchestrator.areaID = cfg.AreaID
		orchestrator.applicationTarget = cfg.ApplicationTarget
		orchestrator.vacancyLimit = cfg.MaxJobs
		orchestrator.minimumScore = cfg.MinimumScore
		orchestrator.applyByScore = cfg.ApplyByScore

		if os.Getenv("KFUMENTOR_QUEUE_ONLY") == "1" {
			return orchestrator.drainManualQueue()
		}
		result, err := orchestrator.ExecuteTask(task, cfg.AutoApply)
		if err != nil {
			if cfg.Debug {
				if _, serr := page.Screenshot(playwright.PageScreenshotOptions{
					Path:     playwright.String("data/screenshots/error_state.png"),
					FullPage: playwright.Bool(true),
				}); serr == nil {
					log.Printf("📸 Скриншот ошибки сохранен: data/screenshots/error_state.png")
				}
			}
			return fmt.Errorf("AI agent error: %w", err)
		}

		log.Printf("✅ AI agent completed task: %s", result)

		report := orchestrator.GetActionHistory()
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))

		return nil
	} else {

		lower := strings.ToLower(task)
		if strings.Contains(lower, "ваканс") || strings.Contains(lower, "hh.ru") || strings.Contains(lower, "hh") {
			query := extractQueryFromTask(task)
			if query == "" {
				query = "AI инженер"
			}
			log.Printf("Extracted search query: %q", query)

			loggedIn := false
			if cfg.ProfileDir != "" {
				if l, lerr := isLoggedIn(page); l && lerr == nil {
					loggedIn = true
				}
			}

			jobs, err := runHHSearch(page, query, loggedIn, cfg.AutoApply, cfg.Debug, cfg.AreaID)
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(jobs, "", "  ")
			fmt.Println(string(b))
			return nil
		}

		if _, err := page.Goto("https://google.com", playwright.PageGotoOptions{Timeout: playwright.Float(30_000)}); err != nil {
			return err
		}
		log.Println("No built-in handler for this task yet.")
		return nil
	}
}

func verifyAndHandleLogin(page playwright.Page, headless bool, profileDir string, autoApply bool) (bool, error) {
	log.Println("Verifying profile login state on hh.ru...")

	urls := []string{"https://hh.ru", "https://hh.ru/applicant"}

	for _, url := range urls {
		if err := navigateWithRetry(page, url, 30_000); err == nil {
			time.Sleep(2 * time.Second)
			clearOverlays(page)

			loggedIn, lerr := isLoggedIn(page)
			if lerr != nil {
				return false, fmt.Errorf("could not determine login state: %w", lerr)
			}

			if loggedIn {
				log.Println("✅ User is already logged in")
				return true, nil
			}
		} else {
			log.Printf("Warning: could not navigate to %s: %v", url, err)
		}
	}

	if headless {
		log.Println("⚠️  Headless режим: проверяю сохраненную сессию...")

		if err := navigateWithRetry(page, "https://hh.ru", 30_000); err == nil {
			time.Sleep(3 * time.Second)
			clearOverlays(page)

			if loggedIn, _ := isLoggedIn(page); loggedIn {
				log.Println("✅ Сессия восстановлена из профиля")
				return true, nil
			}
		}

		log.Println("⚠️  Headless режим: разрешаю работу без логина (только поиск)")
		return false, nil
	}

	log.Println("Profile is not logged-in: opening login page")
	loginURL := "https://hh.ru/account/login"
	if err := navigateWithRetry(page, loginURL, 30_000); err != nil {
		log.Printf("warning: could not open login page %s: %v", loginURL, err)
	}

	if err := page.BringToFront(); err != nil {
		log.Printf("warning: could not bring page to front: %v", err)
	}

	fmt.Println("⏳ Ожидание входа в аккаунт... (5 минут)")
	ok := false
	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		clearOverlays(page)
		if l, _ := isLoggedIn(page); l {
			ok = true
			break
		}
		if i%6 == 0 {
			fmt.Printf("   Прошло %d секунд...\n", i*5)
		}
	}
	if !ok {
		return false, fmt.Errorf("login not completed within timeout (5m)")
	}
	log.Println("✅ Login detected — proceeding with task")
	return true, nil
}

func extractQueryFromTask(task string) string {
	queries := fallbackSearchQueries(task)
	if len(queries) == 0 {
		return ""
	}
	return queries[0]
}

const defaultHHArea = 113

func buildHHSearchURL(query string, preferredArea ...int) string {
	escaped := url.QueryEscape(query)
	areaID := hhAreaForText(query)
	if len(preferredArea) > 0 && preferredArea[0] > 0 {
		areaID = preferredArea[0]
	}
	return fmt.Sprintf("https://hh.ru/search/vacancy?text=%s&area=%d", escaped, areaID)
}

func hhAreaForText(text string) int {
	text = strings.ToLower(text)
	areas := []struct {
		terms []string
		id    int
	}{
		{terms: []string{"москв"}, id: 1},
		{terms: []string{"санкт-петербург", "петербург", "спб"}, id: 2},
		{terms: []string{"екатеринбург"}, id: 3},
		{terms: []string{"новосибирск"}, id: 4},
		{terms: []string{"нижний новгород"}, id: 66},
		{terms: []string{"казан"}, id: 88},
		{terms: []string{"самар"}, id: 78},
		{terms: []string{"краснодар"}, id: 53},
		{terms: []string{"владивосток"}, id: 22},
	}
	for _, area := range areas {
		for _, term := range area.terms {
			if strings.Contains(text, term) {
				return area.id
			}
		}
	}
	return defaultHHArea
}

func navigateWithRetry(page playwright.Page, url string, timeoutMs int) error {
	const attempts = 3
	var lastErr error
	for i := 1; i <= attempts; i++ {
		log.Printf("navigating to %s (attempt %d/%d)", url, i, attempts)

		if i > 1 {
			time.Sleep(time.Duration(1<<uint(i-1)) * time.Second)
		}

		_, err := page.Goto(url, playwright.PageGotoOptions{
			Timeout: playwright.Float(float64(timeoutMs)),
			// hh.ru keeps analytics and background requests open; waiting for
			// full load or network idle can time out after the document is ready.
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		})
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

func clearOverlays(page playwright.Page) {
	selectors := []string{
		"button:has-text(\"Да, верно\")",
		"button:has-text(\"Нет, другой\")",
		"button:has-text(\"Понятно\")",
		"button:has-text(\"Принято\")",
		"button:has-text(\"Принять\")",
		"button:has-text(\"Согласен\")",
		"button:has-text(\"Закрыть\")",
		"button:has-text(\"Нет, спасибо\")",
		"text=Понятно",
		"text=Принято",
		"text=×",
	}

	for _, sel := range selectors {
		loc := page.Locator(sel)
		count, err := loc.Count()
		if err != nil || count == 0 {
			continue
		}
		for i := 0; i < count; i++ {
			el := loc.Nth(i)
			vis, _ := el.IsVisible(playwright.LocatorIsVisibleOptions{Timeout: playwright.Float(60000)})
			if !vis {
				continue
			}
			if err := el.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(60000)}); err != nil {
				log.Printf("clearOverlays: click %s failed: %v", sel, err)
				continue
			}
			log.Printf("clearOverlays: clicked %s", sel)
			time.Sleep(400 * time.Millisecond)
		}
	}

	if err := page.Locator("body").Click(playwright.LocatorClickOptions{Timeout: playwright.Float(10000)}); err == nil {
		time.Sleep(200 * time.Millisecond)
	}
}

func isLoggedIn(page playwright.Page) (bool, error) {
	loginBtn := page.Locator("a:has-text(\"Войти\"), button:has-text(\"Войти\")")
	if c, err := loginBtn.Count(); err == nil && c > 0 {
		for i := 0; i < c; i++ {
			if vis, _ := loginBtn.Nth(i).IsVisible(); vis {
				return false, nil
			}
		}
	}

	profileSelectors := []string{
		"a:has-text(\"Мой профиль\")",
		"a:has-text(\"Профиль\")",
		"img[alt*='аватар']",
		"[data-qa='topbar-account-menu']",
		"[data-qa='main-menu_applicant']",
	}

	for _, sel := range profileSelectors {
		loc := page.Locator(sel)
		if c, err := loc.Count(); err == nil && c > 0 {
			for i := 0; i < c; i++ {
				if vis, _ := loc.Nth(i).IsVisible(); vis {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

func findApplyButton(page playwright.Page) (playwright.Locator, error) {
	selectors := []string{
		"button:has-text(\"Откликнуться\")",
		"button:has-text(\"Откликнуться\")",
		"text=Откликнуться",
		"[data-qa='vacancy-response-link-top']",
		"[data-qa='vacancy-serp__vacancy_response']",
	}

	for _, sel := range selectors {
		loc := page.Locator(sel)
		if c, err := loc.Count(); err == nil && c > 0 {
			for i := 0; i < c; i++ {
				el := loc.Nth(i)
				if vis, _ := el.IsVisible(playwright.LocatorIsVisibleOptions{Timeout: playwright.Float(60000)}); vis {
					return el, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("apply button not found")
}

func applyToVacancy(page playwright.Page, message string) (string, error) {
	clearOverlays(page)
	btn, err := findApplyButton(page)
	if err != nil {
		return "", fmt.Errorf("no apply button: %w", err)
	}

	if err := btn.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(60000)}); err != nil {
		return "", fmt.Errorf("could not click apply button: %w", err)
	}

	time.Sleep(1500 * time.Millisecond)

	if err := fillVisibleMessage(page, message); err != nil {
		return "", fmt.Errorf("сопроводительное не заполнено; отклик не отправлен: %w", err)
	}
	log.Printf("applyToVacancy: filled visible message field")

	submitSelectors := []string{
		"button:has-text(\"Отправить\")",
		"button:has-text(\"Отправить отклик\")",
		"button:has-text(\"Откликнуться\")",
		"text=Отправить",
		"[data-qa='vacancy-response-submit-popup']",
	}

	for _, sel := range submitSelectors {
		sub := page.Locator(sel)
		if c, _ := sub.Count(); c > 0 {
			for i := 0; i < c; i++ {
				el := sub.Nth(i)
				if vis, _ := el.IsVisible(); !vis {
					continue
				}
				if err := el.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(60000)}); err != nil {
					log.Printf("applyToVacancy: click submit %s failed: %v", sel, err)
					continue
				}

				time.Sleep(2000 * time.Millisecond)

				successPatterns := []string{
					"text=/отправлен|успешно|заявка отправлена|спасибо|выполнено/i",
					":has-text(\"отправлена\")",
					":has-text(\"успешно\")",
					"[data-qa='vacancy-response-popup-success']",
				}

				for _, pattern := range successPatterns {
					if c2, _ := page.Locator(pattern).Count(); c2 > 0 {
						return "submitted successfully", nil
					}
				}

				return "submitted (status confirmation not detected)", nil
			}
		}
	}

	return "no submit action detected", nil
}

func findSearchInput(page playwright.Page) (playwright.Locator, error) {
	clearOverlays(page)
	inputs := page.Locator("input[type='search'], input[type='text'], input:not([type])")
	count, err := inputs.Count()
	if err != nil {
		return nil, fmt.Errorf("could not count inputs: %w", err)
	}

	for i := 0; i < count; i++ {
		in := inputs.Nth(i)
		vis, err := in.IsVisible()
		if err != nil || !vis {
			continue
		}
		ph, err := in.GetAttribute("placeholder")
		if err == nil && ph != "" {
			phLower := strings.ToLower(ph)
			if strings.Contains(phLower, "вакан") || strings.Contains(phLower, "поиск") || strings.Contains(phLower, "должн") {
				return in, nil
			}
		}
	}

	for i := 0; i < count; i++ {
		in := inputs.Nth(i)
		vis, err := in.IsVisible()
		if err != nil && vis {
			return in, nil
		}
	}
	return nil, fmt.Errorf("no visible input found")
}

func runHHSearch(page playwright.Page, query string, loggedIn bool, autoApply bool, debug bool, areaID int) ([]Job, error) {
	log.Printf("Opening hh.ru and searching for %q\n", query)
	searchURL := buildHHSearchURL(query, areaID)
	log.Printf("navigating to search url: %s", searchURL)

	if err := navigateWithRetry(page, searchURL, 60_000); err != nil {
		if debug {
			_ = os.MkdirAll("data/screenshots", 0755)
			if _, serr := page.Screenshot(playwright.PageScreenshotOptions{
				Path:     playwright.String("data/screenshots/hh_failed.png"),
				FullPage: playwright.Bool(true),
			}); serr != nil {
				log.Printf("screenshot failed: %v", serr)
			}
			if html, cerr := page.Content(); cerr == nil {
				_ = os.WriteFile("data/screenshots/hh_failed.html", []byte(html), 0644)
			}
		}
		return nil, fmt.Errorf("goto hh.ru: %w", err)
	}

	time.Sleep(1 * time.Second)
	finalURL := page.URL()
	log.Printf("navigated to final url: %s", finalURL)
	clearOverlays(page)

	input, err := findSearchInput(page)
	if err != nil {
		return nil, fmt.Errorf("find search input: %w", err)
	}

	if err := input.Fill(query); err != nil {
		return nil, fmt.Errorf("fill query: %w", err)
	}
	if err := input.Press("Enter"); err != nil {
		return nil, fmt.Errorf("press enter: %w", err)
	}

	time.Sleep(3 * time.Second)

	anchors := page.Locator("a")
	count, err := anchors.Count()
	if err != nil {
		return nil, fmt.Errorf("count anchors: %w", err)
	}

	type candidate struct {
		href  string
		text  string
		score int
	}
	var cands []candidate

	queryLower := strings.ToLower(query)
	keywords := []string{}

	if strings.Contains(queryLower, "python") {
		keywords = append(keywords, "python", "питон", "django", "flask", "fastapi", "web", "backend")
	}
	if strings.Contains(queryLower, "разработчик") || strings.Contains(queryLower, "developer") {
		keywords = append(keywords, "разработчик", "developer", "программист", "engineer", "инженер")
	}
	if strings.Contains(queryLower, "java") {
		keywords = append(keywords, "java", "джава", "spring", "hibernate")
	}
	if strings.Contains(queryLower, "javascript") || strings.Contains(queryLower, "js") {
		keywords = append(keywords, "javascript", "js", "react", "angular", "vue", "node", "frontend")
	}

	if len(keywords) == 0 {
		words := strings.Fields(queryLower)
		keywords = append(keywords, words...)
	}

	excludeKeywords := []string{
		"кассир", "водитель", "логист", "продавец", "охран",
		"грузчик", "монтер", "электрик", "сварщик", "механик",
		"маляр", "уборщик", "повар", "шеф", "официант",
		"администратор гости", "администратор отел",
	}

	for i := 0; i < count; i++ {
		a := anchors.Nth(i)
		vis, err := a.IsVisible()
		if err != nil || !vis {
			continue
		}

		text, err := a.TextContent()
		if err != nil || text == "" {
			continue
		}

		href, err := a.GetAttribute("href")
		if err != nil || href == "" {
			continue
		}

		lt := strings.ToLower(text)
		isExcluded := false
		for _, excl := range excludeKeywords {
			if strings.Contains(lt, excl) {
				isExcluded = true
				break
			}
		}
		if isExcluded {
			continue
		}

		score := 0
		hasKeyword := false

		for _, kw := range keywords {
			if strings.Contains(lt, kw) {
				score += 5
				hasKeyword = true
			}
		}

		if !hasKeyword {
			continue
		}

		if strings.Contains(href, "vacancy") || strings.Contains(href, "vacancies") || strings.Contains(href, "vacans") {
			score += 2
		}

		if score >= 5 {
			cands = append(cands, candidate{
				href:  href,
				text:  strings.TrimSpace(text),
				score: score,
			})
		}
	}

	if len(cands) == 0 {
		log.Println("no candidates found via heuristics, collecting top anchors as fallback")
		for i := 0; i < count && i < 30; i++ {
			a := anchors.Nth(i)
			vis, err := a.IsVisible()
			if err != nil || !vis {
				continue
			}

			text, err := a.TextContent()
			if err != nil || text == "" {
				continue
			}

			href, err := a.GetAttribute("href")
			if err != nil || href == "" {
				continue
			}

			cands = append(cands, candidate{
				href:  href,
				text:  strings.TrimSpace(text),
				score: 1,
			})
		}
	}

	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	seen := map[string]struct{}{}
	jobs := []Job{}

	for _, c := range cands {
		if len(jobs) >= 3 {
			break
		}

		h := c.href
		if strings.HasPrefix(h, "/") {
			h = "https://hh.ru" + h
		}

		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}

		p, err := page.Context().NewPage()
		if err != nil {
			log.Printf("failed to create new page: %v", err)
			continue
		}

		if err := navigateWithRetry(p, h, 90_000); err != nil {
			log.Printf("failed to goto %s: %v", h, err)
			p.Close()
			continue
		}

		time.Sleep(800 * time.Millisecond)
		clearOverlays(p)

		title := c.text
		if t, err := p.Locator("h1").First().TextContent(); err == nil && t != "" {
			title = strings.TrimSpace(t)
		}

		company := ""
		if ctext, err := p.Locator("[data-qa='vacancy-company-name']").First().TextContent(); err == nil && ctext != "" {
			company = strings.TrimSpace(ctext)
		}

		if company == "" {
			divs := p.Locator("div")
			divCount, _ := divs.Count()
			for i := 0; i < divCount; i++ {
				n := divs.Nth(i)
				txt, err := n.TextContent()
				if err == nil && txt != "" {
					txtLower := strings.ToLower(txt)
					if strings.Contains(txtLower, "компан") || strings.Contains(txtLower, "ооо") || strings.Contains(txt, "—") {
						company = strings.TrimSpace(txt)
						break
					}
				}
			}
		}

		snippet := ""
		descSelectors := []string{
			"[data-qa='vacancy-description']",
			"[data-qa='skills-table']",
			"div[class*='description']",
			"article",
		}

		for _, sel := range descSelectors {
			descLoc := p.Locator(sel).First()
			if txt, err := descLoc.TextContent(); err == nil && txt != "" {
				trimmed := strings.TrimSpace(txt)
				if len(trimmed) > 500 {
					snippet = trimmed[:500] + "..."
				} else {
					snippet = trimmed
				}
				break
			}
		}

		if snippet == "" {
			bodyLocator := p.Locator("body")
			if body, err := bodyLocator.TextContent(); err == nil && body != "" {
				trimmed := strings.TrimSpace(body)
				if len(trimmed) > 300 {
					snippet = trimmed[:300] + "..."
				} else {
					snippet = trimmed
				}
			}
		}

		job := Job{
			Title:   title,
			Company: company,
			URL:     h,
			Snippet: snippet,
		}

		if loggedIn && autoApply {
			res, aerr := applyToVacancy(p, "Здравствуйте, заинтересован(а) в этой вакансии. Можете связаться со мной для обсуждения.")
			if aerr != nil {
				log.Printf("applyToVacancy failed for %s: %v", h, aerr)
				job.Applied = false
				job.ApplyResult = aerr.Error()
			} else {
				job.Applied = true
				job.ApplyResult = res
				log.Printf("Applied to %s: %s", h, res)
			}
		}

		jobs = append(jobs, job)
		p.Close()
	}

	return jobs, nil
}

func GetJob(title, company, url, snippet string) Job {
	return types.GetJob(title, company, url, snippet)
}

func isRealVacancyURL(url string) bool {
	if url == "" {
		return false
	}

	urlLower := strings.ToLower(url)

	mustContainPatterns := []string{
		"/vacancy/",
		"vacancy/",
	}

	excludePatterns := []string{
		"/article/",
		"/special/",
		"/partner/",
		"/promo/",
		"?hhtmfrom=",
		"utm_source=",
		"utm_campaign=",
		"lead_gen",
		"gazpromneft",
		"hhtmpromo",
		"from=share_ios",
	}

	hasVacancy := false
	for _, pattern := range mustContainPatterns {
		if strings.Contains(urlLower, pattern) {
			hasVacancy = true
			break
		}
	}

	if !hasVacancy {
		return false
	}

	for _, pattern := range excludePatterns {
		if strings.Contains(urlLower, pattern) {
			return false
		}
	}

	if urlLower == "https://hh.ru" ||
		urlLower == "https://hh.ru/" ||
		strings.Contains(urlLower, "hh.ru/?") ||
		strings.Contains(urlLower, "hh.ru#") {
		return false
	}

	re := regexp.MustCompile(`/vacancy/(\d+)`)
	matches := re.FindStringSubmatch(urlLower)
	if len(matches) < 2 {
		return false
	}

	vacancyID := matches[1]
	if _, err := strconv.Atoi(vacancyID); err != nil {
		return false
	}

	return true
}

func normalizeVacancyURL(url string) string {
	if url == "" {
		return url
	}

	if idx := strings.Index(url, "?"); idx != -1 {
		base := url[:idx]

		query := url[idx+1:]
		params := strings.Split(query, "&")

		var keepParams []string
		for _, param := range params {
			if strings.HasPrefix(param, "query=") ||
				strings.HasPrefix(param, "from=") ||
				strings.HasPrefix(param, "hhtmFromLabel=") {
				keepParams = append(keepParams, param)
			}
		}

		if len(keepParams) > 0 {
			return base + "?" + strings.Join(keepParams, "&")
		}
		return base
	}

	if strings.HasPrefix(url, "//") {
		return "https:" + url
	}

	if strings.HasPrefix(url, "/") {
		return "https://hh.ru" + url
	}

	return url
}

func truncate(s string, maxLen int) string {
	if len([]rune(s)) <= maxLen {
		return s
	}
	return string([]rune(s)[:maxLen]) + "..."
}
