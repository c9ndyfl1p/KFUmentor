package localui

import (
	"encoding/json"
	"github.com/mihnpro/Ai_agent_2/internal/habr"
	playwright "github.com/mxschmitt/playwright-go"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUnifiedSearchBrowser(t *testing.T) {
	if os.Getenv("HABR_BROWSER_TEST") != "1" {
		t.Skip("opt-in isolated browser")
	}
	requests := make(chan map[string]any, 8)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/run":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			body["source"] = "hh"
			requests <- body
			writeJSON(w, 202, map[string]any{})
			return
		case "/api/habr":
			if r.Method == "POST" {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				body["source"] = "habr"
				requests <- body
			}
			writeJSON(w, 200, habrState{MinimumScore: 6, Jobs: []habr.Job{{URL: "https://career.habr.com/vacancies/1", Title: "Habr Analyst", Salary: "100000 ₽", Score: 8, Status: "assessed", Recommendation: "review", ReviewPoints: []string{"Уточнить стаж"}}, {URL: "https://career.habr.com/vacancies/2", Title: "Habr Low", Score: 3, Status: "assessed"}, {URL: "https://career.habr.com/vacancies/3", Title: "Habr Error", Status: "error", Error: "Test error"}}})
			return
		case "/api/state":
			writeJSON(w, 200, taskState{Cards: []vacancyCard{{Index: 1, Title: "HH Analyst", Score: 8, Status: "recommended"}}})
			return
		case "/api/profile", "/api/mentor/plan", "/api/deepseek/status":
			writeJSON(w, 200, map[string]any{})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
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
	web := httptest.NewServer(handler)
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
	p, err := b.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	p.OnPageError(func(err error) { t.Error(err) })
	if _, err = p.Goto(web.URL + "/#search-section"); err != nil {
		t.Fatal(err)
	}
	if err = p.Locator("#habr-cards .vacancy-score").First().WaitFor(); err != nil {
		t.Fatal(err)
	}
	text, _ := p.Locator("#habr-cards").InnerText()
	if !strings.Contains(text, "100000 ₽") || !strings.Contains(text, "Уточнить стаж") || strings.Contains(text, "Habr Low") {
		t.Fatal(text)
	}
	p.Locator("#habr-show-all").Check()
	if err = p.Locator("#habr-cards h3:has-text('Habr Low')").WaitFor(); err != nil {
		t.Fatal(err)
	}
	p.Locator("#task").Fill("SQL analyst")
	check := func(source string) {
		t.Helper()
		select {
		case body := <-requests:
			if body["source"] != source || body["task"] != "SQL analyst" || body["vacancyLimit"] != float64(5) && body["limit"] != float64(5) {
				t.Fatal(body)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing request", source)
		}
	}
	p.Locator("#run-button").Click()
	check("hh")
	p.Locator("#source-habr").Check()
	p.Locator("#source-hh").Uncheck()
	p.Locator("#run-button").Click()
	check("habr")
	p.Locator("#source-hh").Check()
	p.Locator("#run-button").Click()
	check("hh")
	check("habr")
	p.Locator("#habr-cards button:has-text('Повторить оценку')").Click()
	select {
	case body := <-requests:
		if body["retryURL"] != "https://career.habr.com/vacancies/3" {
			t.Fatal(body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing retry")
	}
}
