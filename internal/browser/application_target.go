package browser

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

const applicationSearchPages = 10
const applicationPageSize = 100

var errAlreadyApplied = errors.New("на эту вакансию уже отправлен отклик")

var errNoVacancies = errors.New("вакансий не найдено по данному запросу")

type applicationSummary struct {
	Target, Sent, Scanned, Evaluated, Skipped, Failures, Qualified int
	Reason                                                         string
	Results                                                        []string
}

func (s applicationSummary) String() string {
	status := "Цель достигнута"
	if s.Sent < s.Target {
		status = fmt.Sprintf("Недобор: %d. %s", s.Target-s.Sent, s.Reason)
	}
	return fmt.Sprintf("\n📨 ОТКЛИКИ: %d из %d\nПросмотрено уникальных вакансий: %d\nОценено: %d\nПрошли порог оценки: %d\nПропущено: %d\nОшибок: %d\n%s\n%s", s.Sent, s.Target, s.Scanned, s.Evaluated, s.Qualified, s.Skipped, s.Failures, status, strings.Join(s.Results, "\n"))
}

type applicationOperations struct {
	checkpoint   func() error
	totalSent    func() int
	search       func(string, int) ([]types.Job, error)
	evaluate     func(types.Job) (*JobEvaluationResult, error)
	submit       func(types.Job) (string, error)
	progress     func(int, int)
	canAutoApply func(types.Job) bool
	applyByScore bool
}

// Count successful applications, rather than vacancies inspected or attempts.
// New search pages replenish the pool when jobs do not fit the saved profile.
func runApplicationTarget(target int, queries []string, ops applicationOperations, limits ...int) (applicationSummary, error) {
	summary := applicationSummary{Target: target}
	if target < 1 || target > types.MaxApplicationTarget {
		return summary, fmt.Errorf("количество откликов должно быть от 1 до %d", types.MaxApplicationTarget)
	}
	if len(queries) == 0 {
		return summary, fmt.Errorf("нет поисковых запросов")
	}
	vacancyLimit := 0
	if len(limits) > 0 {
		vacancyLimit = limits[0]
		if vacancyLimit < 1 || vacancyLimit > types.MaxVacancyLimit {
			return summary, fmt.Errorf("лимит поиска вакансий должен быть от 1 до %d", types.MaxVacancyLimit)
		}
	}
	minimumScore := types.DefaultMinimumScore
	if len(limits) > 1 {
		minimumScore = limits[1]
	}
	if minimumScore < 0 || minimumScore > types.MaxMatchScore {
		return summary, fmt.Errorf("минимальная оценка должна быть от 0 до 10")
	}
	scanBudget := 1000
	if vacancyLimit > 0 {
		scanBudget = min(max(vacancyLimit*10, 100), 1000)
	}
	limitReached := func() bool {
		if vacancyLimit > 0 && summary.Qualified >= vacancyLimit {
			summary.Reason = fmt.Sprintf("Достигнут лимит подходящих вакансий: %d из %d прошли порог %d/10.", summary.Qualified, vacancyLimit, minimumScore)
			return true
		}
		if summary.Scanned >= scanBudget {
			summary.Reason = fmt.Sprintf("Достигнут предел проверки: %d вакансий. Измените запрос или настройки поиска.", scanBudget)
			return true
		}
		return false
	}
	seen := map[string]bool{}
	consecutiveEvaluationErrors := 0
	finished := make([]bool, len(queries))
	pageSignatures := make([]map[string]bool, len(queries))
	for i := range queries {
		pageSignatures[i] = map[string]bool{}
	}
	for page := 0; page < applicationSearchPages; page++ {
		active := false
		for index, query := range queries {
			if finished[index] {
				continue
			}
			active = true
			jobs, err := ops.search(query, page)
			if err != nil {
				summary.Reason = "Поиск остановлен из-за ошибки загрузки результатов."
				return summary, err
			}
			if len(jobs) == 0 {
				finished[index] = true
				continue
			}
			var ids []string
			for _, job := range jobs {
				ids = append(ids, applicationJobKey(job.URL))
			}
			sort.Strings(ids)
			signature := strings.Join(ids, "|")
			if pageSignatures[index][signature] {
				finished[index] = true
				continue
			}
			pageSignatures[index][signature] = true
			for _, job := range jobs {
				if ops.checkpoint != nil {
					if err := ops.checkpoint(); err != nil {
						return summary, err
					}
				}
				if ops.totalSent != nil {
					summary.Sent = ops.totalSent()
					if summary.Sent >= target {
						return summary, nil
					}
				}
				key := applicationJobKey(job.URL)
				if key == "" || seen[key] {
					continue
				}
				if limitReached() {
					return summary, nil
				}
				seen[key] = true
				summary.Scanned++
				evaluation, err := ops.evaluate(job)
				if err != nil {
					if errors.Is(err, errUnreadableDescription) {
						summary.Skipped++
						consecutiveEvaluationErrors = 0
						continue
					}
					summary.Failures++
					consecutiveEvaluationErrors++
					summary.Results = append(summary.Results, fmt.Sprintf("❌ %s: ошибка оценки", job.Title))
					if consecutiveEvaluationErrors >= 3 {
						summary.Reason = "Три ошибки оценки подряд: проверьте модель и доступность описаний вакансий."
						return summary, err
					}
					continue
				}
				consecutiveEvaluationErrors = 0
				summary.Evaluated++
				if ops.canAutoApply != nil && !ops.canAutoApply(job) {
					summary.Skipped++
					log.Printf("ℹ️ %s: требуется анкета работодателя; продолжаю поиск", job.Title)
					continue
				}
				if evaluation != nil {
					logEvaluationResult(evaluation)
					if evaluation.MatchScore >= minimumScore {
						summary.Qualified++
					}
				}
				allowed, decisionReason := applicationAllowed(evaluation, minimumScore, ops.applyByScore)
				if !allowed {
					log.Printf("ℹ️ Отклик пропущен: %s", decisionReason)
					summary.Skipped++
					continue
				}
				log.Printf("✅ Решение: откликнуться (оценка: %d/10); %s", evaluation.MatchScore, decisionReason)
				result, err := ops.submit(job)
				if errors.Is(err, errAlreadyApplied) {
					summary.Skipped++
					log.Println("⏭️ Уже откликались: повторный отклик не отправлен")
					continue
				}
				if err != nil {
					summary.Failures++
					log.Printf("❌ Ошибка отклика: %v", err)
					summary.Results = append(summary.Results, fmt.Sprintf("❌ %s: %v", job.Title, err))
					var failure *ApplicationFailure
					if errors.As(err, &failure) && !failure.Sent && !failure.Uncertain {
						log.Println("⏭️ Отклик не отправлялся: сохраняю ошибку карточки и продолжаю поиск")
						continue
					}
					summary.Reason = "Отправка остановлена: результат последнего отклика не подтверждён. Проверьте «Мои отклики» на hh.ru перед повторным запуском."
					return summary, err
				}
				summary.Sent++
				log.Printf("✅ Отклик отправлен! Результат: %s", result)
				summary.Results = append(summary.Results, fmt.Sprintf("✅ %s: %s", job.Title, result))
				if ops.progress != nil {
					ops.progress(summary.Sent, target)
				}
				if summary.Sent == target {
					return summary, nil
				}
			}
		}
		if !active {
			break
		}
	}
	if limitReached() {
		return summary, nil
	}
	summary.Reason = "Подходящих вакансий в просмотренных результатах оказалось меньше цели. Проверено до 10 страниц на каждый поисковый запрос."
	return summary, nil
}

func applicationJobKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if (u.Hostname() != "hh.ru" && !strings.HasSuffix(u.Hostname(), ".hh.ru")) || !strings.HasPrefix(u.Path, "/vacancy/") {
		return ""
	}
	id := strings.Trim(strings.TrimPrefix(u.Path, "/vacancy/"), "/")
	parsed, err := strconv.ParseUint(id, 10, 64)
	if err != nil || parsed == 0 || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return "/vacancy/" + strconv.FormatUint(parsed, 10)
}

func (a *AIOrchestrator) applyUntilTarget(queries []string) (string, error) {
	if !a.autoApply {
		return "", fmt.Errorf("отправка откликов не включена")
	}
	target := a.applicationTarget
	if target == 0 {
		target = types.DefaultApplicationTarget
	}
	log.Printf("🎯 Желаемое количество откликов: %d", target)
	evaluatedIndex := 0
	summary, err := runApplicationTarget(target, queries, applicationOperations{
		applyByScore: a.applyByScore,
		canAutoApply: func(job types.Job) bool {
			if detailed, ok := a.cachedJobDetails(job.URL); ok {
				job = detailed
			}
			return !job.RequiresManualApplication()
		},
		checkpoint: a.drainManualQueue,
		totalSent:  a.workflowTotalCounter(),
		search: func(query string, page int) ([]types.Job, error) {
			log.Printf("🔍 Ищу вакансии: %s, страница %d", query, page+1)
			if _, err := a.searchJobsPage(query, page); err != nil {
				return nil, err
			}
			jobs, err := a.extractJobsFromPage(applicationPageSize)
			if errors.Is(err, errNoVacancies) {
				return nil, nil
			}
			return jobs, err
		},
		evaluate: func(job types.Job) (*JobEvaluationResult, error) {
			evaluatedIndex++
			log.Printf("🤔 Анализирую вакансию %d/%d: %s | URL: %s", evaluatedIndex, 0, job.Title, job.URL)
			evaluation, err := a.evaluateTracked(job)
			if err != nil {
				log.Printf("❌ Ошибка оценки: %v", err)
			} else {
				a.printJobAnalysis(job, evaluation)
			}
			return evaluation, err
		},
		submit: a.submitTracked,
		progress: func(sent, target int) {
			log.Printf("📨 Отправлено откликов: %d из %d", sent, target)
		},
	}, a.vacancyLimit, a.minimumScore)
	log.Print(summary.String())
	return summary.String(), err
}

func logEvaluationResult(e *JobEvaluationResult) {
	reason := strings.Join(strings.Fields(e.Reason), " ")
	log.Printf("✅ Оценка вакансии завершена: оценка: %d/10; рекомендация: %s; причина: %s", e.MatchScore, e.Recommendation, truncateRunes(reason, 240))
}

func applicationAllowed(e *JobEvaluationResult, minimumScore int, byScore bool) (bool, string) {
	if e == nil {
		return false, "нет оценки вакансии"
	}
	if e.MatchScore < 1 || e.MatchScore > 10 || (byScore && e.MatchScore < minimumScore) {
		return false, "оценка ниже выбранного порога или некорректна"
	}
	if e.Recommendation != "apply" && e.Recommendation != "review" && e.Recommendation != "skip" {
		return false, "некорректная рекомендация"
	}
	if byScore {
		return true, "режим «По оценке»: балл проходит выбранный порог"
	}
	if e.Recommendation != "apply" {
		return false, "review требует ручной проверки; автоматическая отправка только для apply"
	}

	return true, "получена рекомендация модели apply"
}

const reviewPointRules = `
Дополнительно верни review_points: массив из 2–3 коротких строк для review: что не подтверждено в профиле, конкретное противоречие (если есть), и один вопрос кандидату. Не выдумывай недостатки. Если спорных моментов нет, выбирай apply; при явном несоответствии выбирай skip. Не копируй recommendation из примера. Желательные навыки не являются обязательными. Для стажировок учитывай учебные проекты, если коммерческий опыт не требуется. Отсутствие сведений не доказывает отсутствие навыка. Для apply/skip допускается пустой review_points. reason и analysis — максимум по одному короткому предложению. Ответ только JSON.`
