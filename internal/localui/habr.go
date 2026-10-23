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
	Running bool       `json:"running"`
	URL     string     `json:"url"`
	Limit   int        `json:"limit"`
	Status  string     `json:"status"`
	Jobs    []habr.Job `json:"jobs"`
	Updated string     `json:"updated"`
}
type habrService struct {
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
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(input.Task) != "" {
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
	h.state = habrState{Running: true, URL: u.String(), Limit: input.Limit, Status: "Собираю вакансии…", Jobs: []habr.Job{}}
	if err = h.save(); err != nil {
		h.state = previous
		writeError(w, 500, "Не удалось сохранить историю: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	h.cancel = cancel
	go func() {
		defer cancel()
		var saveErr error
		err := habr.Collect(ctx, u.String(), input.Limit, func(j habr.Job) {
			if strings.TrimSpace(input.Task) != "" && j.Error == "" {
				job := types.Job{URL: j.URL, Title: j.Title, Company: j.Company, Salary: j.Salary, Snippet: j.Description, DescriptionStatus: "ready"}
				evaluation, evalErr := browser.EvaluateCollectedJob(s.cfg.ModelURL, s.cfg.ModelName, profile, job, input.Task)
				if evalErr != nil {
					j.Error = "Оценка не выполнена: " + evalErr.Error()
				} else {
					j.Score, j.Recommendation, j.Reason = evaluation.MatchScore, evaluation.Recommendation, evaluation.Reason
				}
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			h.state.Jobs = append(h.state.Jobs, j)
			h.state.Status = fmt.Sprintf("Собрано %d из %d", len(h.state.Jobs), h.state.Limit)
			if e := h.save(); e != nil {
				saveErr = e
				cancel()
			}
		})
		h.mu.Lock()
		defer h.mu.Unlock()
		h.state.Running = false
		h.cancel = nil
		h.state.Status = fmt.Sprintf("Сбор завершён. Вакансий: %d", len(h.state.Jobs))
		if err != nil {
			h.state.Status = "Сбор завершён с ошибкой: " + err.Error()
		}
		if ctx.Err() == context.Canceled {
			h.state.Status = "Сбор остановлен; полученные вакансии сохранены"
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
