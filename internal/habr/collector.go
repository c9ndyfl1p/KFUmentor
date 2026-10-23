// Package habr collects public vacancy data without authentication or applications.
package habr

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

//go:embed parse.js
var parseScript string
var vacancyPath = regexp.MustCompile(`^/vacancies/[0-9]+$`)

type Job struct {
	Source       string   `json:"source"`
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Company      string   `json:"company"`
	Salary       string   `json:"salary"`
	Description  string   `json:"description"`
	Requirements []string `json:"requirements"`
	Conditions   []string `json:"conditions"`
	Grade        string   `json:"grade"`
	Published    string   `json:"published"`
	Error        string   `json:"error,omitempty"`
}
type parsed struct {
	Cards  []Job  `json:"cards"`
	Detail *Job   `json:"detail"`
	Next   string `json:"next"`
}

func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != "career.habr.com" || u.User != nil || (u.Path != "/vacancies" && !vacancyPath.MatchString(u.Path)) {
		return nil, fmt.Errorf("Нужна ссылка https://career.habr.com/vacancies с фильтрами или ссылка на вакансию")
	}
	u.Fragment = ""
	return u, nil
}

func fetch(ctx context.Context, raw string) (string, error) {
	if _, err := ValidateURL(raw); err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("слишком много перенаправлений")
		}
		_, err := ValidateURL(req.URL.String())
		return err
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "MatchMentor vacancy collector")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Хабр Карьера вернула HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil {
		return "", err
	}
	if len(b) > 4<<20 {
		return "", fmt.Errorf("страница слишком большая")
	}
	return string(b), nil
}

// Collect preserves all supplied filters and emits results incrementally. Limit
// counts unique vacancies, including cards with a readable per-vacancy error.
func Collect(ctx context.Context, raw string, limit int, emit func(Job)) error {
	u, err := ValidateURL(raw)
	if err != nil {
		return err
	}
	if limit < 1 || limit > 100 {
		return fmt.Errorf("Лимит должен быть от 1 до 100")
	}
	pw, err := playwright.Run()
	if err != nil {
		return err
	}
	defer pw.Stop()
	b, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		return err
	}
	defer b.Close()
	page, err := b.NewPage(playwright.BrowserNewPageOptions{JavaScriptEnabled: playwright.Bool(false)})
	if err != nil {
		return err
	}
	// Parse downloaded HTML in an isolated page. No site scripts or subresources run.
	if err = page.Route("**/*", func(r playwright.Route) { _ = r.Abort() }); err != nil {
		return err
	}
	parse := func(raw string) (parsed, error) {
		var p parsed
		html, err := fetch(ctx, raw)
		if err != nil {
			return p, err
		}
		if err = page.SetContent(html, playwright.PageSetContentOptions{WaitUntil: playwright.WaitUntilStateDomcontentloaded}); err != nil {
			return p, err
		}
		value, err := page.Evaluate(parseScript)
		if err != nil {
			return p, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return p, err
		}
		err = json.Unmarshal(data, &p)
		return p, err
	}
	emitDetail := func(card Job) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw := card.URL
		p, err := parse(raw)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && (p.Detail == nil || p.Detail.Title == "" || p.Detail.Description == "") {
			err = fmt.Errorf("Описание недоступно: вакансия закрыта или страница имеет другой формат")
		}
		if err != nil {
			card.Error = err.Error()
		} else {
			card = *p.Detail
		}
		card.Source = "habr"
		card.URL = raw
		emit(card)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return nil
		}
	}
	if vacancyPath.MatchString(u.Path) {
		return emitDetail(Job{URL: u.String()})
	}
	seen := map[string]bool{}
	pages := map[string]bool{}
	current := u.String()
	for n := 0; n < 20; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if pages[current] {
			return fmt.Errorf("Повтор страницы выдачи; сбор остановлен")
		}
		pages[current] = true
		p, err := parse(current)
		if err != nil {
			return err
		}
		if len(p.Cards) == 0 {
			return fmt.Errorf("Вакансии не найдены или выдача недоступна; проверьте ссылку в браузере")
		}
		fresh := 0
		for _, card := range p.Cards {
			if seen[card.URL] {
				continue
			}
			seen[card.URL] = true
			fresh++
			if err := emitDetail(card); err != nil {
				return err
			}
			if len(seen) >= limit {
				return nil
			}
		}
		if fresh == 0 || p.Next == "" {
			return nil
		}
		next, err := u.Parse(p.Next)
		if err != nil {
			return err
		}
		if _, err = ValidateURL(next.String()); err != nil {
			return err
		}
		// Only the page number comes from pagination; every user filter is retained.
		num, err := strconv.Atoi(next.Query().Get("page"))
		if err != nil || num < 1 {
			return fmt.Errorf("Не удалось прочитать следующую страницу")
		}
		q := u.Query()
		q.Set("page", strconv.Itoa(num))
		u.RawQuery = q.Encode()
		current = u.String()
	}
	return fmt.Errorf("Достигнут предел 20 страниц; сузьте фильтры")
}
