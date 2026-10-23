package localui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mihnpro/Ai_agent_2/internal/browser"
	"github.com/mihnpro/Ai_agent_2/internal/habr"
	"github.com/mihnpro/Ai_agent_2/internal/types"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type habrState struct {
	Task         string     `json:"task"`
	MinimumScore int        `json:"minimumScore"`
	AreaName     string     `json:"areaName"`
	Running      bool       `json:"running"`
	URL          string     `json:"url"`
	Limit        int        `json:"limit"`
	Status       string     `json:"status"`
	Jobs         []habr.Job `json:"jobs"`
	Updated      string     `json:"updated"`
}
type habrService struct {
	collect  func(context.Context, string, int, func(habr.Job)) error
	evaluate func(context.Context, types.Resume, habr.Job, string, string) (*browser.JobEvaluationResult, error)
	resolve  func(context.Context, string) []string

	mu     sync.Mutex
	loaded bool
	state  habrState
	cancel context.CancelFunc
}

const habrHistory = "data/habr-vacancies.local.json"

func (h *habrService) save() error {
	h.state.Updated = time.Now().Format(time.RFC3339)
	b, err := json.MarshalIndent(h.state, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(habrHistory), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(habrHistory), ".habr-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), habrHistory)
}
func (s *server) handleHabr(w http.ResponseWriter, r *http.Request) {
	h := &s.habr
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.loaded {
		b, err := os.ReadFile(habrHistory)
		if err == nil {
			if json.Unmarshal(b, &h.state) != nil {
				h.state.Status = "Не удалось прочитать историю сбора"
			}
		} else if !os.IsNotExist(err) {
			h.state.Status = "Не удалось прочитать историю сбора"
		}
		if h.state.Running {
			h.state.Running = false
			for i := range h.state.Jobs {
				if h.state.Jobs[i].Status == "running" {
					h.state.Jobs[i].Status = "stopped"
				}
			}
			h.state.Status = "Предыдущий сбор прерван; результаты сохранены"
		}
		h.loaded = true
	}
	if r.Method == "GET" {
		writeJSON(w, 200, h.state)
		return
	}
	if r.Method == "DELETE" {
		if h.cancel != nil {
			h.cancel()
			h.state.Status = "Останавливаю сбор…"
		}
		writeJSON(w, 200, h.state)
		return
	}
	if h.state.Running {
		writeError(w, 409, "Сбор уже идёт")
		return
	}
	var input struct {
		URL          string `json:"url"`
		Task         string `json:"task"`
		Limit        int    `json:"limit"`
		MinimumScore int    `json:"minimumScore"`
		AreaName     string `json:"areaName"`
		RetryURL     string `json:"retryURL"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	retry := input.RetryURL != ""
	if retry {
		found := false
		for _, j := range h.state.Jobs {
			if j.URL == input.RetryURL {
				found = true
				break
			}
		}
		if !found {
			writeError(w, 404, "Вакансия отсутствует в истории")
			return
		}
		input.URL, input.Task, input.MinimumScore, input.AreaName = input.RetryURL, h.state.Task, h.state.MinimumScore, h.state.AreaName
		input.Limit = h.state.Limit
	}
	if len([]rune(input.AreaName)) > 200 {
		writeError(w, 400, "Слишком длинное название города")
		return
	}
	if strings.TrimSpace(input.Task) != "" && !retry {
		if len([]rune(input.Task)) > 2000 {
			writeError(w, 400, "Слишком длинный запрос")
			return
		}
		queries := browser.SearchTermsForTask(input.Task)
		if len(queries) == 0 {
			writeError(w, 400, "Уточните профессию или навыки для поиска на Хабре")
			return
		}
		input.URL = "https://career.habr.com/vacancies?q=" + url.QueryEscape(queries[0])
	}
	u, err := habr.ValidateURL(input.URL)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if input.Limit < 1 || input.Limit > 100 {
		writeError(w, 400, "Количество вакансий: от 1 до 100")
		return
	}
	if input.MinimumScore < 0 || input.MinimumScore > 10 {
		writeError(w, 400, "Порог оценки: от 0 до 10")
		return
	}
	var profile types.Resume
	if strings.TrimSpace(input.Task) != "" {
		var profileErr error
		profile, profileErr = s.readProfile()
		if profileErr != nil {
			writeError(w, 500, "Не удалось прочитать профиль")
			return
		}
	}
	previous := h.state
	h.state = habrState{Task: input.Task, MinimumScore: input.MinimumScore, AreaName: input.AreaName, Running: true, URL: u.String(), Limit: input.Limit, Status: "Собираю вакансии…", Jobs: []habr.Job{}}
	if retry {
		h.state.Jobs = previous.Jobs
	}
	if err = h.save(); err != nil {
		h.state = previous
		writeError(w, 500, "Не удалось сохранить историю: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	h.cancel = cancel
	go func() {
		defer cancel()
		// The browser bridge serves one model conversation at a time. Let the hh
		// process finish before Habr starts making model calls.
		for input.Task != "" {
			s.mu.Lock()
			hhRunning := s.state.Running
			s.mu.Unlock()
			if !hhRunning {
				break
			}
			h.mu.Lock()
			h.state.Status = "Жду окончания поиска hh.ru перед оценкой Хабра"
			h.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			if ctx.Err() != nil {
				break
			}
		}
		var saveErr error
		qualified, scanned := 0, 0
		evaluationFailures := 0
		modelPaused := false
		seen := map[string]bool{}
		reached := false
		collectionCtx, stopCollection := context.WithCancel(ctx)
		defer stopCollection()
		urls := []string{u.String()}
		if input.Task != "" && !retry {
			terms := []string{}
			if h.resolve != nil {
				terms = h.resolve(ctx, input.Task)
			} else {
				terms = browser.ResolveSearchTerms(ctx, s.cfg.ModelURL, s.cfg.ModelName, input.Task)
			}
			urls = nil
			for _, term := range terms {
				urls = append(urls, "https://career.habr.com/vacancies?q="+url.QueryEscape(term))
			}
		}
		var err error
		for _, searchURL := range urls {
			budget := 100
			if retry || input.Task == "" {
				budget = input.Limit
			}
			collect := habr.Collect
			if h.collect != nil {
				collect = h.collect
			}
			err = collect(collectionCtx, searchURL, budget, func(j habr.Job) {
				if seen[j.URL] || ctx.Err() != nil || reached {
					return
				}
				seen[j.URL] = true
				scanned++
				if scanned >= 100 {
					defer stopCollection()
				}
				if input.AreaName != "" && !retry && j.Error == "" && !habrMatchesArea(j, input.AreaName) {
					return
				}
				h.mu.Lock()
				index := len(h.state.Jobs)
				if retry {
					for i := range h.state.Jobs {
						if h.state.Jobs[i].URL == j.URL {
							index = i
							break
						}
					}
				}
				j.Status = "running"
				if index == len(h.state.Jobs) {
					h.state.Jobs = append(h.state.Jobs, j)
				} else {
					h.state.Jobs[index] = j
				}
				h.state.Status = fmt.Sprintf("Оцениваю: %s. Подходящих: %d из %d", j.Title, qualified, input.Limit)
				if e := h.save(); e != nil {
					saveErr = e
					cancel()
				}
				h.mu.Unlock()
				if input.Task != "" && j.Error == "" && ctx.Err() == nil {
					job := types.Job{URL: j.URL, Title: j.Title, Company: j.Company, Salary: j.Salary, Location: strings.Join(j.Locations, ", "), WorkFormat: strings.Join(j.Conditions, "; "), Snippet: j.Description + "\nТребования: " + strings.Join(j.Requirements, "; ") + "\nУсловия: " + strings.Join(j.Conditions, "; "), DescriptionStatus: "ready"}
					var evaluation *browser.JobEvaluationResult
					var evalErr error
					if h.evaluate != nil {
						evaluation, evalErr = h.evaluate(ctx, profile, j, input.Task, input.AreaName)
					} else {
						evaluation, evalErr = browser.EvaluateCollectedJob(ctx, s.cfg.ModelURL, s.cfg.ModelName, profile, job, input.Task, input.AreaName)
					}
					if evalErr != nil {
						j.Error = "Оценка не выполнена: " + evalErr.Error()
						evaluationFailures++
						if evaluationFailures >= 2 {
							modelPaused = true
							stopCollection()
						}
					} else {
						evaluationFailures = 0
						j.Score, j.Recommendation, j.Reason = evaluation.MatchScore, evaluation.Recommendation, evaluation.Reason
						j.ReviewPoints, j.Analysis = evaluation.ReviewPoints, evaluation.Analysis
					}
				}
				j.Status = "assessed"
				if j.Error != "" {
					j.Status = "error"
				}
				if ctx.Err() != nil {
					j.Status = "stopped"
				}
				if j.Error == "" && (input.Task == "" || j.Score >= input.MinimumScore) {
					qualified++
				}
				h.mu.Lock()
				h.state.Jobs[index] = j
				h.state.Status = fmt.Sprintf("Просмотрено %d, подходящих %d из %d", scanned, qualified, input.Limit)
				if e := h.save(); e != nil {
					saveErr = e
					cancel()
				}
				h.mu.Unlock()
				if qualified >= input.Limit {
					reached = true
					stopCollection()
				}
			})
			if reached || modelPaused || ctx.Err() != nil || scanned >= 100 || retry {
				break
			}
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		h.state.Running = false
		h.cancel = nil
		h.state.Status = fmt.Sprintf("Поиск завершён. Подходящих: %d из %d; просмотрено: %d", qualified, input.Limit, scanned)
		if retry {
			h.state.Status = "Повторная оценка завершена; остальные карточки сохранены"
		}
		if err != nil && !reached && scanned < 100 {
			h.state.Status = "Сбор завершён с ошибкой: " + err.Error()
		}
		if ctx.Err() == context.Canceled {
			h.state.Status = "Сбор остановлен; полученные вакансии сохранены"
		}
		if modelPaused {
			h.state.Status = "Поиск приостановлен после двух ошибок модели подряд. Проверьте подключение и повторите оценку карточек."
		}
		if saveErr != nil {
			h.state.Status = "Ошибка сохранения результатов: " + saveErr.Error()
		}
		if e := h.save(); e != nil {
			h.state.Status = "Ошибка сохранения результатов: " + e.Error()
		}
	}()
	writeJSON(w, http.StatusAccepted, h.state)
}

// Only an explicit city or remote condition can pass an area constraint.
func habrMatchesArea(j habr.Job, area string) bool {
	for _, city := range j.Locations {
		if strings.EqualFold(strings.TrimSpace(city), strings.TrimSpace(area)) {
			return true
		}
	}
	for _, condition := range j.Conditions {
		if strings.Contains(strings.ToLower(condition), "удалён") || strings.Contains(strings.ToLower(condition), "удален") {
			return true
		}
	}
	return false
}
