package browser

import (
	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
	"os"
	"testing"
)

func TestSalaryExtractionBrowser(t *testing.T) {
	if os.Getenv("KFUMENTOR_PROGRESS_SMOKE") == "" {
		t.Skip("opt-in local browser fixture")
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
	page, err := b.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, html, want string }{
		{"range", `<div data-qa="vacancy-salary">от 80 000 до 120 000 ₽
за месяц, на руки</div>`, "от 80 000 до 120 000 ₽ за месяц, на руки"},
		{"gross", `<div data-qa="vacancy-salary-compensation-type-gross">до 2 000 $ до вычета налогов</div>`, "до 2 000 $ до вычета налогов"},
		{"missing", `<div>Опыт от 3 лет, команда 100 человек</div>`, ""},
		{"hidden", `<div hidden data-qa="vacancy-salary">999 ₽</div>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := page.SetContent(`<h1>Вакансия</h1>` + tc.html + `<div data-qa="vacancy-description">Описание вакансии</div>`); err != nil {
				t.Fatal(err)
			}
			job, err := readJobDetails(page, types.Job{})
			if err != nil {
				t.Fatal(err)
			}
			if job.Salary != tc.want {
				t.Fatalf("salary=%q, want %q", job.Salary, tc.want)
			}
		})
	}
}
