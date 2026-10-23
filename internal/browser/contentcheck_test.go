package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/contentcheck"
	playwright "github.com/mxschmitt/playwright-go"
)

type outgoingModel func(context.Context, string, string, json.RawMessage) (string, error)

func (f outgoingModel) GenerateStructured(c context.Context, p, s string, j json.RawMessage) (string, error) {
	return f(c, p, s, j)
}
func TestEditedLetterCannotReuseContentApproval(t *testing.T) {
	calls := 0
	a := &AIOrchestrator{moderation: &contentcheck.Checker{Model: outgoingModel(func(_ context.Context, p, s string, _ json.RawMessage) (string, error) {
		calls++
		var v struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		json.Unmarshal([]byte(p), &v)
		if strings.Contains(v.Text, "Посторонний призыв") {
			return `{"decision":"reject","kind":"other","reasons":["Постороннее содержание"]}`, nil
		}
		return `{"decision":"allow","kind":"` + v.Kind + `","reasons":[]}`, nil
	})}}
	letter := "Здравствуйте! Рассматриваю вакансию аналитика. Среди моих навыков SQL."
	if err := a.checkOutgoingContent(letter); err != nil {
		t.Fatal(err)
	}
	if err := a.checkOutgoingContent(letter); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("unchanged profile/letter not cached", calls)
	}
	a.approvedLetterVerified = true
	if a.checkOutgoingContent(letter+" Посторонний призыв") == nil {
		t.Fatal("edited letter reused approval")
	}
	a.resume.Summary = "Посторонний призыв"
	if a.checkOutgoingContent(letter) == nil {
		t.Fatal("changed profile reused approval")
	}
}
func TestModerationBlocksClickEvenForConfirmedLetter(t *testing.T) {
	if os.Getenv("KFUMENTOR_MODERATION_BROWSER_SMOKE") != "1" {
		t.Skip("opt-in isolated browser fixture")
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
	for _, mode := range []string{"reject_resume", "reject_letter", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			ctx, err := b.NewContext()
			if err != nil {
				t.Fatal(err)
			}
			defer ctx.Close()
			clicked := make(chan bool, 1)
			ctx.Route("**/*", func(route playwright.Route) {
				if strings.Contains(route.Request().URL(), "capture") {
					clicked <- true
					route.Fulfill(playwright.RouteFulfillOptions{Body: "ok"})
					return
				}
				route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: `<h1>Аналитик</h1><div data-qa="vacancy-description">SQL аналитика</div><button onclick="fetch('/capture')">Откликнуться</button>`})
			})
			a := &AIOrchestrator{context: ctx, approvedLetter: "Здравствуйте! Рассматриваю вакансию аналитика. Среди моих навыков SQL.", approvedLetterVerified: true, jobDetails: map[string]Job{}}
			a.moderation = &contentcheck.Checker{Model: outgoingModel(func(_ context.Context, p, _ string, _ json.RawMessage) (string, error) {
				if mode == "unavailable" {
					return "", errors.New("HTTP 429")
				}
				var input struct {
					Kind string `json:"kind"`
				}
				json.Unmarshal([]byte(p), &input)
				if mode == "reject_resume" || input.Kind == "cover_letter" {
					return `{"decision":"reject","kind":"other","reasons":["Постороннее содержание"]}`, nil
				}
				return `{"decision":"allow","kind":"resume","reasons":[]}`, nil
			})}
			_, err = a.performJobApplication(targetJob(1))
			if err == nil || !strings.Contains(err.Error(), "проверка содержания") {
				t.Fatal(err)
			}
			var failure *ApplicationFailure
			if !errors.As(err, &failure) || failure.Sent || failure.Uncertain {
				t.Fatal("rejected content incorrectly marked as sent/uncertain", err)
			}
			select {
			case <-clicked:
				t.Fatal("clicked despite rejected content")
			default:
			}
		})
	}
}
