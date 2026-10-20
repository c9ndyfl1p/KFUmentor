package browser

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/prompt"
	"github.com/mihnpro/Ai_agent_2/internal/types"
	"github.com/mihnpro/Ai_agent_2/internal/workflow"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestAutoApplicationRecoversFromLetterRejectionOnFixture(t *testing.T) {
	if os.Getenv("KFUMENTOR_RECOVERY_BROWSER_SMOKE") == "" {
		t.Skip("opt-in local fixture; all hh.ru traffic is intercepted")
	}
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", filepath.Join(t.TempDir(), "history.json"))
	t.Setenv("KFUMENTOR_RUN_ID", "run")
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
	defer ctx.Close()
	var mu sync.Mutex
	var clicks []string
	var sent []string
	ctx.Route("**/*", func(route playwright.Route) {
		u, _ := url.Parse(route.Request().URL())
		if u.Path == "/apply-click" {
			mu.Lock()
			clicks = append(clicks, u.Query().Get("id"))
			mu.Unlock()
			route.Fulfill(playwright.RouteFulfillOptions{Body: "ok"})
			return
		}
		if u.Path == "/capture" {
			text, _ := route.Request().PostData()
			mu.Lock()
			sent = append(sent, text)
			mu.Unlock()
			route.Fulfill(playwright.RouteFulfillOptions{Body: "ok"})
			return
		}
		id := strings.TrimPrefix(u.Path, "/vacancy/")
		html := `<h1>Проверка ` + id + `</h1><div data-qa="vacancy-company-name">Fixture</div><div data-qa="vacancy-description">SQL analytics</div><button onclick="fetch('/apply-click?id=` + id + `');document.querySelector('section').hidden=false">Откликнуться</button><section hidden><textarea name="message"></textarea><button onclick="fetch('/capture',{method:'POST',body:document.querySelector('textarea').value});document.querySelector('#success').textContent='Отклик отправлен'">Отправить отклик</button></section><div id="success"></div>`
		route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: html})
	})
	goodLetter := "Здравствуйте! Рассматриваю вакансию аналитика. Среди моих навыков SQL. Буду рад обсудить задачи позиции."
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		var req llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&req)
		answer := goodLetter
		if req.System == prompt.LetterReviewSystem {
			answer = `{"supported":true,"unsupported_claims":[]}`
			if strings.Contains(req.Prompt, "пять лет") {
				answer = `{"supported":false,"unsupported_claims":["пять лет коммерческого опыта"]}`
			}
		} else if strings.Contains(req.Prompt, `"title":"Проверка 1"`) {
			answer = "Здравствуйте! Рассматриваю вакансию аналитика. У меня пять лет коммерческого опыта с SQL. Готов обсудить задачи."
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: answer})
	})
	a.context = ctx
	a.resume = types.Resume{Summary: "Начинающий аналитик без коммерческого опыта", Skills: []string{"SQL"}}
	jobs := []types.Job{targetJob(1), targetJob(2)}
	store := currentStore()
	if err := store.Update(func(st *workflow.State) error {
		st.Runs["run"] = &workflow.Run{Target: 1}
		for i, job := range jobs {
			st.Entries[workflow.JobID(job.URL)] = &workflow.Entry{ID: workflow.JobID(job.URL), Job: job, RunID: "run", Status: "assessed", Score: 8, Threshold: 6, Recommendation: "apply"}
			jobs[i].Title = "Проверка " + workflow.JobID(job.URL)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := runApplicationTarget(1, []string{"SQL"}, applicationOperations{
		search: func(_ string, page int) ([]types.Job, error) {
			if page == 0 {
				return jobs, nil
			}
			return nil, nil
		}, evaluate: relevantJob, submit: a.submitTracked, totalSent: a.totalSent,
	})
	if err != nil || summary.Sent != 1 || summary.Failures != 1 {
		t.Fatal(summary, err)
	}
	st, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if st.Entries["1"].Status != "draft_error" || st.Entries["1"].Submission != "" || !strings.Contains(st.Entries["1"].Error, "неподтверждённые") || st.Entries["2"].Submission != "sent" || st.Runs["run"].Sent != 1 {
		t.Fatal(st)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(clicks) != 1 || clicks[0] != "2" || len(sent) != 1 || sent[0] != goodLetter {
		t.Fatal("unexpected external actions", clicks, sent)
	}
}
