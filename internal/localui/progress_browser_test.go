package localui

import (
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
		{Index: 1, Title: "Ручная проверка", Score: 6, Status: "review", Result: "Нужна ручная проверка"},
		{Index: 2, Title: "Низкая оценка", Score: 3, Status: "skipped"},
		{Index: 3, Title: "Ошибка модели", Status: "error", Details: "Нет ответа"},
		{Index: 4, Title: "В работе", Status: "running"},
	}}}
	mux := http.NewServeMux()
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
	for _, expected := range []string{"Ручная проверка", "Ошибка модели", "В работе"} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing card", expected, text)
		}
	}
	if strings.Contains(text, "Низкая оценка") {
		t.Fatal("threshold ignored")
	}
	if text, err := page.Locator("#task-summary").InnerText(); err != nil || !strings.Contains(text, "0 из 2") {
		t.Fatal(text, err)
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
