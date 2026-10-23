package habr

import (
	"context"
	"encoding/json"
	playwright "github.com/mxschmitt/playwright-go"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateURL(t *testing.T) {
	for _, u := range []string{"http://career.habr.com/vacancies", "https://evil.com/vacancies", "https://career.habr.com.evil.com/vacancies", "https://career.habr.com:443/vacancies", "https://user@career.habr.com/vacancies", "https://career.habr.com/vacancies/1/external_response"} {
		if _, e := ValidateURL(u); e == nil {
			t.Errorf("accepted %s", u)
		}
	}
	for _, u := range []string{"https://career.habr.com/vacancies?qid=4&s[]=44&s[]=125", "https://career.habr.com/vacancies/1000166002"} {
		if _, e := ValidateURL(u); e != nil {
			t.Fatal(e)
		}
	}
}
func TestParserFixture(t *testing.T) {
	if os.Getenv("HABR_BROWSER_TEST") != "1" {
		t.Skip("opt-in isolated DOM fixture")
	}
	pw, e := playwright.Run()
	if e != nil {
		t.Fatal(e)
	}
	defer pw.Stop()
	b, e := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, e := b.NewPage()
	if e != nil {
		t.Fatal(e)
	}
	e = p.SetContent(`<div class="vacancy-header"><h1>Analyst</h1><div class="vacancy-header__salary"><div class="predicted-salary">Похожие специалисты получают 200000</div></div><div class="vacancy-company__title"><a class="link-comp">Employer</a></div></div><div class="vacancy-description__text"><p>SQL required</p><p>Python optional</p><form>Private form</form></div><div class="vacancy-card"><a class="vacancy-card__title-link" href="/vacancies/123">Role</a><span class="vacancy-card__salary">от 100000 ₽</span></div><a rel="next" href="/vacancies?page=2">Next</a>`)
	if e != nil {
		t.Fatal(e)
	}
	v, e := p.Evaluate(parseScript)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(v)
	var result parsed
	if e = json.Unmarshal(data, &result); e != nil {
		t.Fatal(e)
	}
	if result.Detail.Salary != "" || result.Detail.Company != "Employer" || strings.Contains(result.Detail.Description, "Private") || !strings.Contains(result.Detail.Description, "\n") || len(result.Cards) != 1 || result.Cards[0].Salary != "от 100000 ₽" || result.Next == "" {
		t.Fatalf("incorrect parse: %+v", result)
	}
}
func TestLiveReadOnly(t *testing.T) {
	if os.Getenv("HABR_LIVE_TEST") != "1" {
		t.Skip("opt-in public read-only collection")
	}
	urls := []string{"https://career.habr.com/vacancies/1000166002", "https://career.habr.com/vacancies/1000168651", "https://career.habr.com/vacancies/1000167897", "https://career.habr.com/vacancies?qid=4&remote=true&s[]=44&s[]=125&s[]=177&s[]=175&s[]=126&type=all"}
	for _, u := range urls {
		t.Run(u, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			count := 0
			e := Collect(ctx, u, 2, func(j Job) {
				count++
				if j.Error != "" || j.Title == "" || j.Company == "" || len(j.Description) < 100 {
					t.Errorf("incomplete: %+v", j)
				}
				t.Logf("%s | %s | salary=%q | %d chars", j.Title, j.Company, j.Salary, len(j.Description))
			})
			if e != nil || count == 0 || count > 2 {
				t.Fatalf("count=%d err=%v", count, e)
			}
		})
	}
}
