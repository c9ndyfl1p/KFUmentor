package browser

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

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
	a.detailLoadMu.Lock()
	defer a.detailLoadMu.Unlock()
	return a.loadJobDetailsLocked(job)
}

func (a *AIOrchestrator) loadJobDetailsLocked(job types.Job) (types.Job, error) {
	if cached, ok := a.cachedJobDetails(job.URL); ok && cached.DescriptionStatus != "unreadable" {
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
		if captchaPresent(page) {
			if captchaErr := a.waitForCaptcha(page); captchaErr != nil {
				return job, captchaErr
			}
			return a.loadJobDetailsLocked(job)
		}
		return job, fmt.Errorf("загрузить описание вакансии: %w", err)
	}
	if err := a.handleCaptchaIfPresent(page); err != nil {
		return job, err
	}
	if page.IsClosed() {
		return a.loadJobDetailsLocked(job)
	}
	detailed, err := readJobDetails(page, job)
	a.cacheJobDetails(job.URL, detailed)
	return detailed, err
}

func (a *AIOrchestrator) cachedJobDetails(url string) (types.Job, bool) {
	a.jobDetailsMu.RLock()
	defer a.jobDetailsMu.RUnlock()
	job, ok := a.jobDetails[url]
	return job, ok
}

func (a *AIOrchestrator) cacheJobDetails(url string, job types.Job) {
	a.jobDetailsMu.Lock()
	defer a.jobDetailsMu.Unlock()
	if a.jobDetails == nil {
		a.jobDetails = map[string]types.Job{}
	}
	a.jobDetails[url] = job
}

var errUnreadableDescription = errors.New("не удалось прочитать описание вакансии")

func classifyJobPage(page playwright.Page, job types.Job) types.Job {
	finalURL, _ := url.Parse(page.URL())
	original, _ := url.Parse(job.URL)
	job.ApplicationMode = "standard"
	job.ApplicationURL = ""
	if page.URL() == "about:blank" {
		return job
	}
	if finalURL == nil || finalURL.Scheme != "https" || (finalURL.Hostname() != "hh.ru" && !strings.HasSuffix(finalURL.Hostname(), ".hh.ru")) {
		job.ApplicationMode = "external"
	} else if strings.HasPrefix(finalURL.Path, "/vrsurvey/") || strings.HasPrefix(finalURL.Path, "/article/") || visibleAny(page, []string{"button:has-text('Отправить анкету')", "input[type='submit'][value*='анкету']"}) {
		job.ApplicationMode = "questionnaire"
		job.ApplicationURL = finalURL.String()
	} else if original != nil && !strings.HasPrefix(finalURL.Path, "/vacancy/") {
		job.ApplicationMode = "unsupported"
	}
	return job
}

func readJobDetails(page playwright.Page, job types.Job) (types.Job, error) {
	job.DescriptionStatus = "unreadable"
	job = classifyJobPage(page, job)
	if salary := readJobSalary(page); salary != "" {
		job.Salary = salary
	}
	selectors := []string{"[data-qa='vacancy-description']", ".tmpl_hh_description", "[class*='vacancy-description--']", ".vacancy-description"}
	deadline := time.Now().Add(15 * time.Second)
	if job.RequiresManualApplication() {
		deadline = time.Now().Add(6 * time.Second)
	}
	var text string
	for {
		if captchaPresent(page) {
			return job, fmt.Errorf("нужна проверка CAPTCHA в браузере hh.ru")
		}
		if strings.Contains(page.URL(), "/account/login") || strings.Contains(page.URL(), "/account/signup") {
			return job, fmt.Errorf("нужен вход в hh.ru")
		}
		if job.ApplicationMode == "external" {
			return job, fmt.Errorf("%w: переход на внешний сайт; откройте вакансию вручную", errUnreadableDescription)
		}
		for _, selector := range selectors {
			items := page.Locator(selector)
			count, _ := items.Count()
			for i := 0; i < count; i++ {
				item := items.Nth(i)
				if visible, _ := item.IsVisible(); !visible {
					continue
				}
				value, err := item.Evaluate(`el => {
     function read(node) {
      if (node.nodeType===Node.TEXT_NODE) return node.textContent;
      if (node.nodeType!==Node.ELEMENT_NODE) return '';
      if (node.matches('script,style,template,form,input,textarea,select,button,[contenteditable],nav,footer')) return '';
      const style=getComputedStyle(node);
      if(style.display==='none'||style.visibility==='hidden') return '';
      if(node.tagName==='BR') return '\n';
      const text=Array.from(node.childNodes).map(read).join('');
      return /^(P|DIV|LI|UL|OL|H[1-6]|SECTION|ARTICLE)$/.test(node.tagName) ? '\n'+text+'\n' : text;
     }
     return read(el);
    }`, nil)
				if err == nil {
					if value, ok := value.(string); ok && strings.TrimSpace(value) != "" {
						text = strings.TrimSpace(value)
						break
					}
				}
			}
			if text != "" {
				break
			}
		}
		if text != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
		job = classifyJobPage(page, job)
	}
	if text == "" {
		if job.ApplicationMode == "standard" {
			job.ApplicationMode = "unsupported"
		}
		return job, fmt.Errorf("%w: формат страницы не поддерживается или описание не загрузилось; откройте страницу вручную", errUnreadableDescription)
	}
	if len([]rune(text)) > 40000 {
		return job, fmt.Errorf("%w: описание превышает 40 000 символов; нужна ручная проверка без усечения требований", errUnreadableDescription)
	}
	var cleanLines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			cleanLines = append(cleanLines, line)
		}
	}
	text = strings.Join(cleanLines, "\n")
	job.ExperienceRequirement = firstVisibleJobText(page, []string{"[data-qa='vacancy-experience']", "[data-qa='work-experience-text']"})
	job.Employment = firstVisibleJobText(page, []string{"[data-qa='common-employment-text']"})
	job.WorkFormat = firstVisibleJobText(page, []string{"[data-qa='work-formats-text']"})
	job.Location = firstVisibleJobText(page, []string{"[data-qa='vacancy-view-raw-address']", "[data-qa='vacancy-address-with-map']"})
	job.Snippet = text
	if salary := readJobSalary(page); salary != "" {
		job.Salary = salary
	}
	job.DescriptionStatus = "ready"
	if job.ApplicationMode == "questionnaire" && job.Salary == "" {
		job.Salary = salaryFromDescription(text)
	}
	// A landing page heading may be an ad slogan; retain the search result title.
	if job.ApplicationMode == "standard" {
		titles := page.Locator("h1")
		if count, _ := titles.Count(); count > 0 {
			if title, err := titles.First().InnerText(); err == nil && strings.TrimSpace(title) != "" {
				job.Title = strings.TrimSpace(title)
			}
		}
	}
	company := page.Locator("[data-qa='vacancy-company-name'], .vacancy-company-name").First()
	if count, _ := company.Count(); count > 0 {
		if text, err := company.InnerText(); err == nil {
			job.Company = strings.TrimSpace(text)
		}
	}
	return job, nil
}

var incomeWords = regexp.MustCompile(`(?i)(зарплат|заработн|зп|доход|оклад|стоимость километра|суточные)`)
var moneyAmount = regexp.MustCompile(`(?i)\d[\d\s.,–—-]*(₽|руб|₸|тенге|€|евро|\$|доллар)`)

func salaryFromDescription(description string) string {
	var lines []string
	for _, line := range strings.Split(description, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if len([]rune(line)) <= 500 && incomeWords.MatchString(line) && moneyAmount.MatchString(line) {
			lines = append(lines, line)
		}
		if len(lines) == 3 {
			break
		}
	}
	return strings.Join(lines, "; ")
}

// Preserve the employer's wording, including currency, period and tax terms.
// Restrict extraction to salary elements: numbers in the description are not pay.
func readJobSalary(page playwright.Page) string {
	for _, selector := range []string{"[data-qa='vacancy-salary']", "[data-qa='vacancy-salary-compensation-type-net']", "[data-qa='vacancy-salary-compensation-type-gross']"} {
		items := page.Locator(selector)
		count, _ := items.Count()
		for i := 0; i < count; i++ {
			item := items.Nth(i)
			if visible, _ := item.IsVisible(); !visible {
				continue
			}
			if text, err := item.InnerText(); err == nil {
				if text = strings.Join(strings.Fields(text), " "); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func firstVisibleJobText(page playwright.Page, selectors []string) string {
	for _, selector := range selectors {
		items := page.Locator(selector)
		count, _ := items.Count()
		for i := 0; i < count; i++ {
			item := items.Nth(i)
			if visible, _ := item.IsVisible(); !visible {
				continue
			}
			if text, err := item.InnerText(); err == nil && strings.TrimSpace(text) != "" {
				return strings.Join(strings.Fields(text), " ")
			}
		}
	}
	return ""
}
