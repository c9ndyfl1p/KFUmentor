package localui

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/workflow"
)

func (s *server) initWorkflow() error {
	path, err := filepath.Abs("data/application-history.local.json")
	if err != nil {
		return err
	}
	s.history = &workflow.Store{Path: path}
	if err := s.history.Recover(); err != nil {
		return err
	}
	st, err := s.history.Read()
	if err != nil {
		return err
	}
	s.runID = st.LastRun
	if len(st.Entries) > 0 {
		s.state.Status = "История загружена"
		now := time.Now()
		s.state.StartedAt = &now
	}
	return nil
}
func workflowCards(st workflow.State) []vacancyCard {
	entries := make([]*workflow.Entry, 0, len(st.Entries))
	for _, e := range st.Entries {
		if e.Status != "dismissed" && !e.Hidden {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].UpdatedAt.Before(entries[j].UpdatedAt) })
	cards := make([]vacancyCard, 0, len(entries))
	for i, e := range entries {
		status := e.Status
		stage := e.Stage
		switch status {
		case "evaluating":
			status = "running"
		case "queued_draft", "drafting", "queued_send", "queued_retry":
			status = "queued"
		case "draft_ready":
			status = "ready"
		case "draft_error":
			status = "error"
		case "assessed":
			status = "recommended"
		}
		c := vacancyCard{ID: e.ID, Index: i + 1, Title: e.Job.Title, Salary: e.Job.Salary, DescriptionStatus: e.Job.DescriptionStatus, URL: safeVacancyURL(e.Job.URL), Status: status, Stage: stage, Score: e.Score, Summary: e.Reason, Details: e.Error, ReviewPoints: e.ReviewPoints, Submission: e.Submission, Letter: e.Letter, Version: e.Version, CanDraft: e.CanDraft(), CanSend: e.Status == "draft_ready" && e.Submission == "", CanRetry: e.Status == "error" && e.Submission == ""}
		if e.Job.RequiresManualApplication() && e.Submission == "" {
			c.CanDraft = false
			c.CanSend = false
			c.ApplicationURL = safeApplicationURL(e.Job.ApplicationURL)
			if c.ApplicationURL == "" {
				c.ApplicationURL = c.URL
			}
			c.CanRetry = e.Job.DescriptionStatus == "ready" && e.Error != "" && (e.Status == "questionnaire" || e.Status == "error")
			if e.Job.ApplicationMode == "questionnaire" {
				c.Status = "questionnaire"
				c.Stage = "Анкета работодателя — требуется заполнение"
			} else {
				c.Status = "unreadable"
				c.Stage = "Не удалось прочитать описание"
			}
		}
		if c.Details == "" {
			c.Details = e.LetterWarning
		}
		budget := e.RunID
		if st.ActiveRun != "" {
			budget = st.ActiveRun
		}
		if run := st.Runs[budget]; run != nil && run.Target > 0 && run.Sent >= run.Target && e.Submission == "" {
			c.CanDraft = false
			c.CanSend = false
			c.Stage = "Общая цель по откликам достигнута"
		}
		if e.Recommendation == "review" {
			c.Result = "Рекомендация модели: нужна проверка"
		}
		if e.Submission == "sent" {
			c.Result = "Отклик и сопроводительное отправлены"
		} else if e.Submission == "without_letter" {
			c.Result = "Отклик отправлен без письма; проверьте чат hh.ru"
		}
		cards = append(cards, c)
	}
	return cards
}
func (s *server) handleVacancyAction(w http.ResponseWriter, r *http.Request) {
	if s.history == nil {
		writeError(w, 503, "История недоступна")
		return
	}
	var input struct {
		ID      string `json:"id"`
		Action  string `json:"action"`
		Version string `json:"version"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, 400, "Некорректное действие")
		return
	}
	if input.Action == "dismiss" {
		err := s.history.Update(func(st *workflow.State) error {
			e := st.Entries[input.ID]
			if e == nil {
				return fmt.Errorf("вакансия не найдена")
			}
			if e.Submission != "" || !(e.Status == "assessed" || e.Status == "review" || e.Status == "draft_ready" || e.Status == "draft_error") {
				return fmt.Errorf("действие уже выполняется или недоступно")
			}
			e.Status = "dismissed"
			return nil
		})
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"status": "Пропущена"})
		return
	}
	if err := s.history.Enqueue(input.ID, input.Action, input.Version); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	s.kickManualWorker()
	writeJSON(w, 202, map[string]string{"status": "Действие поставлено в очередь"})
}
func (s *server) kickManualWorker() {
	if s.history == nil {
		return
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	st, err := s.history.Read()
	if err != nil || len(st.Queue) == 0 {
		return
	}
	if !s.beginTask("Выполняю ручное действие…", true) {
		return
	}
	args := s.taskArguments(taskInput{Task: "Ручная обработка сохранённых вакансий", Visible: true, MinimumScore: s.cfg.MinimumScore, ApplicationTarget: 1, VacancyLimit: 1})
	if err := s.launchCommand(args, "Ручное действие завершено", true); err != nil {
		s.failQueuedActions(err.Error())
		s.setFinished("Не удалось запустить ручное действие", err.Error())
	}
}
func (s *server) failQueuedActions(detail string) {
	if s.history == nil {
		return
	}
	_ = s.history.Update(func(st *workflow.State) error {
		for _, c := range st.Queue {
			if e := st.Entries[c.EntryID]; e != nil {
				e.Status = "error"
				if c.Kind != "retry" {
					e.Status = "draft_error"
				}
				e.Stage = "Ручное действие не выполнено"
				e.Error = detail
			}
		}
		st.Queue = nil
		return nil
	})
}
func (s *server) finishWorkflow() {
	if s.history == nil {
		return
	}
	_ = s.history.Update(func(st *workflow.State) error {
		st.ActiveRun = ""
		for _, e := range st.Entries {
			if e.Submission == "sending" {
				e.Submission = "uncertain"
				e.Status = "error"
				e.Stage = "Отправка прервана; проверьте hh.ru"
				e.Error = "Повторная отправка заблокирована"
			}
			if e.Status == "evaluating" {
				e.Status = "error"
				e.Stage = "Оценка прервана"
				e.Error = "Можно повторить оценку"
			}
			if e.Status == "drafting" {
				e.Status = "draft_error"
				e.Stage = "Подготовка письма прервана"
			}
		}
		return nil
	})
}
