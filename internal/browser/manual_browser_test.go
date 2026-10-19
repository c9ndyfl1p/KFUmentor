package browser

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestConfirmedLetterIsSentUnchangedOnFixture(t *testing.T) {
	if os.Getenv("KFUMENTOR_MANUAL_BROWSER_SMOKE") == "" {
		t.Skip("opt-in browser fixture; no network to hh.ru")
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
	for _, tc := range []struct {
		name              string
		supported, cached bool
	}{
		{"verified", true, false}, {"unsupported", false, false}, {"unchanged_checked_draft", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supported := tc.supported
			ctx, err := b.NewContext()
			if err != nil {
				t.Fatal(err)
			}
			defer ctx.Close()
			submitted := make(chan string, 1)
			ctx.Route("**/*", func(route playwright.Route) {
				if strings.Contains(route.Request().URL(), "/capture") {
					data, _ := route.Request().PostData()
					submitted <- data
					route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(200), Body: "ok"})
					return
				}
				route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: `<h1>Analyst</h1><div data-qa="vacancy-company-name">Fixture company</div><div data-qa="vacancy-description">SQL analytics</div><button onclick="document.querySelector('section').hidden=false">Откликнуться</button><section hidden><textarea name="message"></textarea><button onclick="fetch('/capture',{method:'POST',body:document.querySelector('textarea').value});document.querySelector('#success').textContent='Отклик отправлен'">Отправить отклик</button></section><div id="success"></div>`})
			})
			a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.cached {
					t.Error("unchanged checked draft was checked again")
				}
				var req llm.GenerateRequest
				json.NewDecoder(r.Body).Decode(&req)
				if req.Format != "json" {
					t.Error("approved letter was regenerated")
				}
				review := `{"supported":true,"unsupported_claims":[]}`
				if !supported {
					review = `{"supported":false,"unsupported_claims":["unsupported"]}`
				}
				json.NewEncoder(w).Encode(llm.GenerateResponse{Response: review})
			})
			letter := "Здравствуйте! Рассматриваю вакансию аналитика. Среди моих навыков SQL. Готов обсудить задачи позиции."
			a.context = ctx
			a.approvedLetter = letter
			a.approvedLetterVerified = tc.cached
			_, err = a.performJobApplication(targetJob(1))
			if supported {
				if err != nil {
					t.Fatal(err)
				}
				select {
				case text := <-submitted:
					if text != letter {
						t.Fatal("letter changed", text)
					}
				default:
					t.Fatal("not submitted")
				}
			}
			if !supported {
				if err == nil {
					t.Fatal("unsupported letter accepted")
				}
				select {
				case <-submitted:
					t.Fatal("unsupported letter submitted")
				default:
				}
			}
		})
	}
}
