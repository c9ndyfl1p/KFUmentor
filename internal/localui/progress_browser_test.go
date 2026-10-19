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
	if _, err := page.Goto(web.URL); err != nil {
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
}
