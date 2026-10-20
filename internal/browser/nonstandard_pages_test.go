package browser

import (
	"encoding/json"
	"errors"
	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/types"
	"github.com/mihnpro/Ai_agent_2/internal/workflow"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestNonstandardPageFixtures(t *testing.T) {
	if os.Getenv("KFUMENTOR_FORMS_SMOKE") == "" {
		t.Skip("opt-in isolated pages")
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
	cases := []struct{ name, path, html, mode, salary, contains, errorText string }{
		{"standard", "/vacancy/1", `<h1>Analyst</h1><span data-qa="vacancy-experience">не требуется</span><span data-qa="common-employment-text">Полная занятость</span><span data-qa="work-formats-text">Формат работы: на месте работодателя</span><span data-qa="vacancy-view-raw-address">Чебоксары</span><div hidden data-qa="vacancy-description">Hidden wrong text</div><div data-qa="vacancy-description">Требуется SQL</div><div data-qa="vacancy-salary">80 000 ₽ за месяц</div>`, "standard", "80 000 ₽ за месяц", "SQL", ""},
		{"article", "/article/31727", `<div class="tmpl_hh_description"><b>Условия:</b><ul><li>Средняя зп за вахту 230 000 - 250 000 руб;</li></ul><b>Требования:</b><p>Права категории Е</p><form>PRIVATE<input value="PRIVATE"></form><p hidden>PRIVATE</p></div><form><input name="phone" value="PRIVATE"><button>Отправить анкету</button></form>`, "questionnaire", "Средняя зп за вахту 230 000 - 250 000 руб;", "Права категории Е", ""},
		{"survey", "/vrsurvey/iteko_drivers", `<div class="vacancy-description--abc"><p>Водитель категории Е</p><p>На испытательный срок гарантированный доход 6000 руб./сутки + суточные</p></div><form>PRIVATE<button>Отправить анкету</button></form>`, "questionnaire", "На испытательный срок гарантированный доход 6000 руб./сутки + суточные", "Водитель категории Е", ""},
		{"embedded_form", "/vacancy/1", `<div data-qa="vacancy-description">Требуется SQL</div><form><button>Отправить анкету</button></form>`, "questionnaire", "", "SQL", ""},
		{"unknown", "/article/777", `<main>Неизвестный формат</main>`, "questionnaire", "", "", "не удалось прочитать"},
		{"login", "/account/login", `<input name="phone">`, "unsupported", "", "", "нужен вход"},
		{"captcha", "/vacancy/1", `<div data-qa="captcha">Подтвердите, что вы не робот</div>`, "standard", "", "", "CAPTCHA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, err := b.NewContext()
			if err != nil {
				t.Fatal(err)
			}
			defer ctx.Close()
			ctx.Route("**/*", func(route playwright.Route) {
				route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: tc.html})
			})
			page, err := ctx.NewPage()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = page.Goto("https://hh.ru" + tc.path); err != nil {
				t.Fatal(err)
			}
			job, err := readJobDetails(page, types.Job{URL: "https://hh.ru/vacancy/1", Title: "Original title"})
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("error: %v", err)
				}
				if job.DescriptionStatus != "unreadable" {
					t.Fatal(job)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if job.ApplicationMode != tc.mode || job.Salary != tc.salary || !strings.Contains(job.Snippet, tc.contains) || strings.Contains(job.Snippet, "PRIVATE") || job.DescriptionStatus != "ready" {
				t.Fatalf("wrong extraction: %+v", job)
			}
			if tc.name == "standard" && (job.ExperienceRequirement != "не требуется" || job.Location != "Чебоксары" || job.WorkFormat == "") {
				t.Fatal("header lost", job)
			}
			if tc.mode == "questionnaire" {
				a := &AIOrchestrator{context: ctx, approvedLetter: "This must not be sent", approvedLetterVerified: true}
				_, err = a.performJobApplication(types.Job{URL: "https://hh.ru" + tc.path})
				var failure *ApplicationFailure
				if !errors.As(err, &failure) || failure.Sent || failure.Uncertain {
					t.Fatalf("unsafe submission state: %v", err)
				}
			}
		})
	}
}

func TestUnreadablePagesDoNotStopApplicationSearch(t *testing.T) {
	submitted := 0
	jobs := []types.Job{targetJob(1), targetJob(2), targetJob(3), targetJob(4), targetJob(5)}
	result, err := runApplicationTarget(1, []string{"SQL"}, applicationOperations{
		search: func(_ string, page int) ([]types.Job, error) {
			if page == 0 {
				return jobs, nil
			}
			return nil, nil
		},
		evaluate: func(job types.Job) (*JobEvaluationResult, error) {
			if job.URL != jobs[4].URL {
				return nil, errUnreadableDescription
			}
			return &JobEvaluationResult{MatchScore: 8, Recommendation: "apply"}, nil
		},
		submit: func(types.Job) (string, error) { submitted++; return "fixture", nil },
	})
	if err != nil || result.Sent != 1 || submitted != 1 {
		t.Fatal(result, err)
	}
}

func TestQuestionnaireDoesNotUseApplicationBudget(t *testing.T) {
	jobs := []types.Job{targetJob(1), targetJob(2)}
	sent := 0
	result, err := runApplicationTarget(1, []string{"SQL"}, applicationOperations{
		search: func(_ string, page int) ([]types.Job, error) {
			if page == 0 {
				return jobs, nil
			}
			return nil, nil
		},
		evaluate: func(types.Job) (*JobEvaluationResult, error) {
			return &JobEvaluationResult{MatchScore: 9, Recommendation: "apply"}, nil
		},
		canAutoApply: func(job types.Job) bool { return job.URL == jobs[1].URL },
		submit: func(job types.Job) (string, error) {
			if job.URL != jobs[1].URL {
				t.Fatal("submitted questionnaire")
			}
			sent++
			return "fixture", nil
		},
	}, 1, 6)
	if err != nil || result.Sent != 1 || sent != 1 || result.Qualified != 1 {
		t.Fatal(result, err)
	}
}

func TestQuestionnaireEvaluationPersistsAndCannotQueueSend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", path)
	job := types.Job{URL: "https://hh.ru/vacancy/1", Title: "Driver", ApplicationMode: "questionnaire", ApplicationURL: "https://hh.ru/article/31727", DescriptionStatus: "ready", Salary: "230 000 руб за вахту"}
	a := &AIOrchestrator{jobDetails: map[string]types.Job{job.URL: job}, minimumScore: 6}
	if err := a.rememberJob(types.Job{URL: job.URL}, &JobEvaluationResult{MatchScore: 8, Recommendation: "apply"}, nil, false); err != nil {
		t.Fatal(err)
	}
	store := &workflow.Store{Path: path}
	st, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry := st.Entries["1"]
	if entry.Status != "questionnaire" || entry.Score != 8 || entry.Job.Salary != job.Salary || entry.Submission != "" {
		t.Fatal(entry)
	}
	if err := store.Enqueue("1", "draft", ""); err == nil {
		t.Fatal("questionnaire accepted draft")
	}
	if err := store.Update(func(st *workflow.State) error {
		st.Entries["1"].Status = "draft_ready"
		st.Entries["1"].Version = "v"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue("1", "send", "v"); err == nil {
		t.Fatal("questionnaire accepted send")
	}
}

func TestQuestionnaireGoesThroughModelAndPersists(t *testing.T) {
	if os.Getenv("KFUMENTOR_FORMS_SMOKE") == "" {
		t.Skip("local fixture")
	}
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", filepath.Join(t.TempDir(), "history.json"))
	calls := 0
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), "Чебоксары") {
			t.Error("missing saved city in evaluation")
		}
		if strings.Contains(string(data), "PRIVATE") {
			t.Error("form data leaked to model")
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: `{"role_match":"direct","mandatory_gaps":[],"is_relevant":true,"reason":"Подтверждены SQL и учебные проекты","match_score":8,"recommendation":"apply","analysis":"SQL соответствует требованиям","requirements":["SQL"],"skills_match":["SQL"],"review_points":[],"blockers_checked":true,"hard_blockers":[]}`})
	})
	a.resume = types.Resume{Skills: []string{"SQL"}}
	a.areaID = 107
	a.areaName = "Чебоксары"
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
	ctx, err := b.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	a.context = ctx
	ctx.Route("**/*", func(route playwright.Route) {
		route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: `<div class="tmpl_hh_description"><p>Требуется SQL.</p><p>Доход 80 000 руб за месяц.</p></div><form>PRIVATE<input value="PRIVATE"><button>Отправить анкету</button></form>`})
	})
	evaluation, err := a.evaluateTracked(types.Job{Title: "Analyst", URL: "https://hh.ru/vacancy/1"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || evaluation.MatchScore != 8 {
		t.Fatal(calls, evaluation)
	}
	st, err := currentStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	e := st.Entries["1"]
	if e.Status != "questionnaire" || e.Score != 8 || e.Job.ApplicationURL != "https://hh.ru/vacancy/1" || e.Submission != "" {
		t.Fatal(e)
	}
	if e.SearchContext.AreaName != "Чебоксары" {
		t.Fatal("context not persisted", e.SearchContext)
	}
	currentStore().Update(func(st *workflow.State) error { st.Entries["1"].Status = "error"; return nil })
	if err := currentStore().Enqueue("1", "retry", ""); err != nil {
		t.Fatal(err)
	}
	a.areaID = 1
	a.areaName = "Москва"
	if err := a.drainManualQueue(); err != nil {
		t.Fatal(err)
	}
	if a.areaName != "Москва" {
		t.Fatal("retry leaked area context")
	}
	st, _ = currentStore().Read()
	if st.Entries["1"].SearchContext.AreaName != "Чебоксары" || st.Entries["1"].Score != 8 {
		t.Fatal(st.Entries["1"])
	}

}
