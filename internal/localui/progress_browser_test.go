package localui

import (
	"encoding/json"
	playwright "github.com/mxschmitt/playwright-go"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestProgressBrowser(t *testing.T) {
	if os.Getenv("KFUMENTOR_PROGRESS_SMOKE") == "" {
		t.Skip("opt-in local browser test")
	}
	s := &server{state: taskState{MinimumScore: 6, Status: "Поиск завершён", Summary: "📨 ОТКЛИКИ: 0 из 2", Cards: []vacancyCard{
		{ID: "123", CanDraft: true, ReviewPoints: []string{"Не подтверждён опыт", "Готовы к работе в офисе?"}, Index: 1, Title: "Ручная проверка", Salary: "от 80 000 ₽ за месяц, на руки", Score: 6, Status: "review", Result: "Нужна ручная проверка"},
		{Index: 2, Title: "Низкая оценка", Score: 3, Status: "skipped"},
		{Index: 3, Title: "Ошибка модели", Status: "error", Details: "Нет ответа"},
		{Index: 4, Title: "В работе", Status: "running"},
		{Index: 5, Title: "Отправлено ниже порога", Score: 3, Status: "success", Submission: "sent"},
		{Index: 6, Title: "Без письма ниже порога", Score: 2, Status: "warning", Submission: "without_letter"},
		{Index: 7, Title: "Ошибка письма ниже порога", Score: 4, Status: "error", Details: "Письмо не подготовлено"},
		{Index: 8, Title: "Результат неизвестен", Status: "error", Submission: "uncertain"},
		{Index: 9, Title: "Отклик уже существует", Score: 1, Status: "skipped", Submission: "already"},
		{Index: 10, Title: "Сохранённый черновик ниже порога", Score: 3, Status: "ready"},
		{ID: "form", Index: 11, Title: "Анкета водителя", Status: "questionnaire", Score: 8, Salary: "230 000 руб за вахту", ApplicationURL: "https://hh.ru/article/31727", Stage: "Анкета работодателя — требуется заполнение"},
		{ID: "unknown", Index: 12, Title: "Неизвестный формат", Status: "unreadable", DescriptionStatus: "unreadable", ApplicationURL: "https://hh.ru/vacancy/12"},
	}}}
	actions := make(chan string, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/vacancy/action", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ ID, Action, Version string }
		json.NewDecoder(r.Body).Decode(&body)
		actions <- body.Action
		s.mu.Lock()
		c := &s.state.Cards[0]
		if body.Action == "draft" {
			c.Letter = "Тестовое письмо, не отправляемое работодателям."
			c.Version = "test-v1"
			c.CanSend = true
			c.Status = "ready"
		}
		if body.Action == "send" {
			if body.Version != "test-v1" {
				t.Error("wrong preview version", body.Version)
			}
			c.CanSend = false
			c.CanDraft = false
			c.Status = "queued"
		}
		s.mu.Unlock()
		writeJSON(w, 202, map[string]string{"status": "test only"})
	})
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/mentor/plan", s.handleGetMentorPlan)
	mux.HandleFunc("GET /api/profile", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{}) })
	mux.HandleFunc("GET /api/deepseek/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"captchaRequired": false})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if r.URL.Path == "/" {
			name = "static/index.html"
		}
		data, err := staticFiles.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(name, ".js") {
			w.Header().Set("Content-Type", "application/javascript")
		}
		if strings.HasSuffix(name, ".css") {
			w.Header().Set("Content-Type", "text/css")
		}
		w.Write(data)
	})
	web := httptest.NewServer(mux)
	defer web.Close()
	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	b, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	page, err := b.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	page.OnPageError(func(err error) { t.Error(err) })
	if _, err := page.Goto(web.URL + "/#search-section"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#advanced-settings summary").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(".vacancy-progress-card").First().WaitFor(); err != nil {
		t.Fatal(err)
	}
	cards := page.Locator("#vacancy-cards")
	text, err := cards.InnerText()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"от 80 000 ₽ за месяц, на руки", "Зарплата не указана", "Ручная проверка", "Ошибка модели", "В работе", "Отправлено ниже порога", "Без письма ниже порога", "Ошибка письма ниже порога", "Результат неизвестен", "Отклик уже существует", "Сохранённый черновик ниже порога"} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing card", expected, text)
		}
	}
	if !strings.Contains(text, "230 000 руб за вахту") || !strings.Contains(text, "Не удалось получить зарплату") {
		t.Fatal("missing nonstandard salary state", text)
	}
	form := page.Locator("article").Filter(playwright.LocatorFilterOptions{HasText: "Анкета водителя"})
	if count, _ := form.Locator("a:has-text('Открыть анкету')").Count(); count != 1 {
		t.Fatal("missing questionnaire link")
	}
	if count, _ := form.Locator("button:has-text('Откликнуться'), button:has-text('Повторить оценку')").Count(); count != 0 {
		t.Fatal("questionnaire shows invalid actions")
	}
	if strings.Contains(text, "Низкая оценка") {
		t.Fatal("threshold ignored")
	}
	if text, err := page.Locator("#task-summary").InnerText(); err != nil || !strings.Contains(text, "0 из 2") {
		t.Fatal(text, err)
	}
	if checked, err := page.Locator("#application-policy").IsChecked(); err != nil || !checked {
		t.Fatal("strict mode must be initial default", err)
	}
	if err := page.Locator("#application-policy").Uncheck(); err != nil {
		t.Fatal(err)
	}
	if _, err := page.Reload(); err != nil {
		t.Fatal(err)
	}
	if checked, err := page.Locator("#application-policy").IsChecked(); err != nil || checked {
		t.Fatal("policy did not persist", err)
	}
	if err := page.Locator("#vacancy-cards button:has-text('Откликнуться')").Click(); err != nil {
		t.Fatal(err)
	}
	if action := <-actions; action != "draft" {
		t.Fatal("preview sent an application", action)
	}
	if err := page.Locator("#application-letter").WaitFor(); err != nil {
		t.Fatal(err)
	}
	if _, err := page.WaitForFunction("() => !document.querySelector('#application-send').disabled", nil); err != nil {
		t.Fatal(err)
	}
	if text, err := page.Locator("#application-letter").InnerText(); err != nil || !strings.Contains(text, "Тестовое письмо") {
		t.Fatal(text, err)
	}
	// Reproduce an old failed letter still present while its replacement is queued.
	s.mu.Lock()
	s.state.Cards[0].Status = "queued"
	s.state.Cards[0].Stage = "Письмо в очереди"
	s.state.Cards[0].Details = "Старая ошибка проверки"
	s.state.Cards[0].CanSend = false
	s.mu.Unlock()
	if _, err := page.Evaluate("() => refreshState()"); err != nil {
		t.Fatal(err)
	}
	if text, err := page.Locator("#application-letter").InnerText(); err != nil || !strings.Contains(text, "Готовится новая версия") || strings.Contains(text, "Тестовое письмо") {
		t.Fatal(text, err)
	}
	if text, err := page.Locator("#application-stage").InnerText(); err != nil || text != "Письмо в очереди" {
		t.Fatal(text, err)
	}
	if disabled, err := page.Locator("#application-send").IsDisabled(); err != nil || !disabled {
		t.Fatal("queued draft was sendable", err)
	}
	s.mu.Lock()
	s.state.Cards[0].Status = "ready"
	s.state.Cards[0].Details = ""
	s.state.Cards[0].Stage = "Проверьте письмо"
	s.state.Cards[0].CanSend = true
	s.mu.Unlock()
	if _, err := page.Evaluate("() => refreshState()"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#application-send").Click(); err != nil {
		t.Fatal(err)
	}
	if action := <-actions; action != "send" {
		t.Fatal(action)
	}
	if err := page.Locator("#application-close").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("[data-app-section='mentor']").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#mentor-tab-plan").Click(); err != nil {
		t.Fatal(err)
	}
	if selected, err := page.Locator("#mentor-tab-plan").GetAttribute("aria-selected"); err != nil || selected != "true" {
		t.Fatal("mentor tab did not activate", selected, err)
	}
	if err := page.Locator("[data-app-section='profile']").Click(); err != nil {
		t.Fatal(err)
	}
	if hidden, err := page.Locator("#profile-form").IsHidden(); err != nil || !hidden {
		t.Fatal("profile should open in preview mode", err)
	}
	if err := page.Locator("#edit-profile-button").Click(); err != nil {
		t.Fatal(err)
	}
	if visible, err := page.Locator("#profile-form").IsVisible(); err != nil || !visible {
		t.Fatal("profile editor did not open", err)
	}
	if err := page.Locator("#cancel-profile-edit").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("[data-app-section='hr']").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#hr-reply-form").WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible}); err != nil {
		t.Fatal("HR workspace did not open", err)
	}
	if err := page.SetViewportSize(390, 844); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ hash, selector string }{
		{"#overview-section", "#overview-section"},
		{"#search-section", "#search-section"},
		{"#hr-section", "#hr-reply-form"},
		{"#mentor-section", ".mentor-tabs"},
		{"#profile-section", "#profile-read-view"},
	} {
		if _, err := page.Evaluate("hash => { location.hash = hash; }", route.hash); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(route.selector).WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible}); err != nil {
			t.Fatal("mobile route did not open", route.hash, err)
		}
		widthFits, err := page.Evaluate("() => document.documentElement.scrollWidth <= window.innerWidth")
		if err != nil || widthFits != true {
			diagnostics, _ := page.Evaluate("() => ({viewport:window.innerWidth,doc:document.documentElement.scrollWidth,body:document.body.scrollWidth})")
			t.Fatal("mobile layout has horizontal overflow", route.hash, diagnostics, err)
		}
	}
}
