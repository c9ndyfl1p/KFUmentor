package browser

import (
	"errors"
	"fmt"
	"github.com/mihnpro/Ai_agent_2/internal/contentcheck"
	playwright "github.com/mxschmitt/playwright-go"
	"os"
	"strings"
	"testing"
)

func TestAuditHiddenSuccessMustNotConfirmRejectedApplication(t *testing.T) {
	if os.Getenv("KFUMENTOR_AUDIT_BROWSER_SMOKE") == "" {
		t.Skip("local fake browser audit")
	}
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", "")
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
	ctx.Route("**/*", func(route playwright.Route) {
		route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: `<h1>Аналитик</h1><div data-qa="vacancy-company-name">Fixture</div><div data-qa="vacancy-description">SQL</div><div data-qa="vacancy-response-success" hidden>Отклик отправлен</div><button onclick="document.querySelector('section').hidden=false">Откликнуться</button><section hidden><textarea name="message"></textarea><button onclick="document.querySelector('#error').textContent='Не удалось отправить отклик'">Отправить отклик</button></section><div id="error"></div>`})
	})
	a := &AIOrchestrator{moderation: &contentcheck.Checker{Model: allowContentModel{}}, context: ctx, approvedLetter: "Здравствуйте! Рассматриваю вакансию аналитика. Среди моих навыков SQL. Буду рад обсудить задачи позиции.", approvedLetterVerified: true}
	result, err := a.performJobApplication(targetJob(1))
	if err == nil {
		t.Fatal("False success: page only had a hidden success marker; simulated submit displayed an error; application not sent. Result:", result)
	}
}

func TestChatLetterRequiresSavedMessage(t *testing.T) {
	if os.Getenv("KFUMENTOR_AUDIT_BROWSER_SMOKE") == "" {
		t.Skip("local fixture")
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
	for _, save := range []bool{false, true} {
		t.Run(fmt.Sprint(save), func(t *testing.T) {
			ctx, err := b.NewContext()
			if err != nil {
				t.Fatal(err)
			}
			defer ctx.Close()
			action := ""
			if save {
				action = "document.querySelector('#messages').textContent=document.querySelector('textarea').value"
			}
			ctx.Route("**/*", func(route playwright.Route) {
				route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: `<h1>Analyst</h1><div data-qa="vacancy-description">SQL</div><button onclick="document.querySelector('#accepted').hidden=false;document.querySelector('#add').hidden=false">Откликнуться</button><div id="accepted" data-qa="vacancy-response-success" hidden>Отклик отправлен</div><button id="add" hidden onclick="document.querySelector('section').hidden=false">Добавить сопроводительное</button><section hidden><textarea name="message"></textarea><button onclick="` + action + `">Отправить</button></section><div id="messages"></div>`})
			})
			a := &AIOrchestrator{moderation: &contentcheck.Checker{Model: allowContentModel{}}, context: ctx, approvedLetter: "Здравствуйте! Рассматриваю вакансию аналитика. Среди моих навыков SQL. Буду рад обсудить задачи позиции.", approvedLetterVerified: true}
			_, err = a.performJobApplication(targetJob(1))
			if save && err != nil {
				t.Fatal(err)
			}
			if !save {
				var failure *ApplicationFailure
				if !errors.As(err, &failure) || !failure.Sent {
					t.Fatalf("unconfirmed letter must be warning after confirmed bare response: %v", err)
				}
			}
		})
	}
}

func TestResponseCardRequiresVacancyID(t *testing.T) {
	if os.Getenv("KFUMENTOR_AUDIT_BROWSER_SMOKE") == "" {
		t.Skip("local fixture")
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
	page.Route("**/*", func(route playwright.Route) {
		route.Fulfill(playwright.RouteFulfillOptions{Body: `<a href="/vacancy/999">Analyst</a><button>Добавить сопроводительное</button>`})
	})
	if err := openResponseCard(page, "https://hh.ru/vacancy/123", "Analyst", ""); err == nil {
		t.Fatal("matched unrelated vacancy by title")
	}
}

func TestResponseCardOpensMatchingChatNotVacancy(t *testing.T) {
	if os.Getenv("KFUMENTOR_AUDIT_BROWSER_SMOKE") == "" {
		t.Skip("local fixture")
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
	page.Route("**/*", func(route playwright.Route) {
		route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html; charset=utf-8"), Body: `<article><a href="/vacancy/999">Analyst</a><button onclick="document.body.dataset.chat='wrong'">Перейти в чат</button></article><article><a href="/vacancy/123">Analyst</a><button onclick="document.body.dataset.chat='123';document.querySelector('#add').hidden=false">Перейти в чат</button></article><button id="add" hidden>Добавить сопроводительное</button>`})
	})
	if err := openResponseCard(page, "https://hh.ru/vacancy/123", "Analyst", ""); err != nil {
		t.Fatal(err)
	}
	chat, err := page.Locator("body").GetAttribute("data-chat")
	if err != nil || chat != "123" {
		t.Fatalf("wrong chat: %q %v", chat, err)
	}
	if !strings.Contains(page.URL(), "/applicant/vacancy_response") {
		t.Fatal("navigated to vacancy instead of chat")
	}
}
