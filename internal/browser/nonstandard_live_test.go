package browser

import (
	"os"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
)

// Read-only public pages in a fresh browser; no profile, model calls or forms.
func TestKnownEmployerPagesReadOnly(t *testing.T) {
	if os.Getenv("KFUMENTOR_FORMS_LIVE") != "1" {
		t.Skip("opt-in read-only hh.ru compatibility check")
	}
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
	for _, tc := range []struct{ name, url, mode, word string }{
		{"Credit", "https://kazan.hh.ru/vacancy/137779786", "standard", "кредит"},
		{"MTS", "https://kazan.hh.ru/vacancy/136738018", "standard", "клиент"},
		{"TBank", "https://kazan.hh.ru/vacancy/138310875", "standard", "клиент"},
		{"Loginof", "https://kazan.hh.ru/article/31727?utm_redirect_vacancy_id=138468617", "questionnaire", "вахту"},
		{"ITECO", "https://kazan.hh.ru/vrsurvey/iteko_drivers?utm_redirect_vacancy_id=138395260", "questionnaire", "суточные"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := b.NewPage()
			if err != nil {
				t.Fatal(err)
			}
			defer page.Close()
			page.Route("**/*", func(route playwright.Route) {
				if method := route.Request().Method(); method != "GET" && method != "HEAD" {
					route.Abort()
					return
				}
				route.Continue()
			})
			if _, err = page.Goto(tc.url, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateDomcontentloaded, Timeout: playwright.Float(45000)}); err != nil {
				t.Fatal(err)
			}
			job, err := readJobDetails(page, types.Job{URL: tc.url, Title: tc.name})
			if err != nil {
				t.Fatal(err)
			}
			if job.ApplicationMode != tc.mode || !strings.Contains(strings.ToLower(job.Snippet), tc.word) {
				t.Fatalf("mode=%s; expected relevant description", job.ApplicationMode)
			}
			if tc.name == "Credit" && (job.ExperienceRequirement != "не требуется" || !strings.Contains(job.Location, "Чебоксары") || job.WorkFormat == "") {
				t.Fatalf("vacancy header lost: experience=%q location=%q format=%q", job.ExperienceRequirement, job.Location, job.WorkFormat)
			}
			if tc.name == "Loginof" && !strings.Contains(job.Salary, "вахту") {
				t.Fatalf("pay period lost: %q", job.Salary)
			}
			t.Logf("mode=%s description=%d runes salary=%q", job.ApplicationMode, len([]rune(job.Snippet)), job.Salary)
		})
	}
}
