package browser

import (
	"fmt"
	"strings"

	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
)

func mergeJobs(existing, added []types.Job) []types.Job {
	seen := map[string]bool{}
	for _, job := range existing {
		seen[job.URL] = true
	}
	for _, job := range added {
		if job.URL == "" || seen[job.URL] {
			continue
		}
		seen[job.URL] = true
		existing = append(existing, job)
	}
	return existing
}

// Alternate queries so the final limit does not discard every result from the
// second and third search. Keep a single vacancy when synonyms overlap.
func selectSearchResults(groups [][]types.Job, limit int) []types.Job {
	var jobs []types.Job
	seen := map[string]bool{}
	for index := 0; len(jobs) < limit; index++ {
		hadItems := false
		for _, group := range groups {
			if index >= len(group) {
				continue
			}
			hadItems = true
			job := group[index]
			if job.URL == "" || seen[job.URL] {
				continue
			}
			seen[job.URL] = true
			jobs = append(jobs, job)
			if len(jobs) == limit {
				break
			}
		}
		if !hadItems {
			break
		}
	}
	return jobs
}

func (a *AIOrchestrator) loadJobDetails(job types.Job) (types.Job, error) {
	if cached, ok := a.jobDetails[job.URL]; ok {
		return cached, nil
	}
	page, err := a.context.NewPage()
	if err != nil {
		return job, fmt.Errorf("открыть описание вакансии: %w", err)
	}
	defer page.Close()
	if _, err = page.Goto(job.URL, playwright.PageGotoOptions{
		Timeout: playwright.Float(30000), WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return job, fmt.Errorf("загрузить описание вакансии: %w", err)
	}
	detailed, err := readJobDetails(page, job)
	if err == nil {
		if a.jobDetails == nil {
			a.jobDetails = map[string]types.Job{}
		}
		a.jobDetails[job.URL] = detailed
	}
	return detailed, err
}

func readJobDetails(page playwright.Page, job types.Job) (types.Job, error) {
	description := page.Locator("[data-qa='vacancy-description'], .vacancy-description").First()
	if err := description.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible, Timeout: playwright.Float(15000),
	}); err != nil {
		return job, fmt.Errorf("описание вакансии недоступно (возможны вход или CAPTCHA): %w", err)
	}
	text, err := description.InnerText()
	if err != nil || strings.TrimSpace(text) == "" {
		return job, fmt.Errorf("описание вакансии пустое")
	}
	job.Snippet = truncateRunes(text, 10000)
	if title, err := page.Locator("h1").First().InnerText(); err == nil && strings.TrimSpace(title) != "" {
		job.Title = strings.TrimSpace(title)
	}
	company := page.Locator("[data-qa='vacancy-company-name'], .vacancy-company-name").First()
	if count, _ := company.Count(); count > 0 {
		if text, err := company.InnerText(); err == nil {
			job.Company = strings.TrimSpace(text)
		}
	}
	return job, nil
}
