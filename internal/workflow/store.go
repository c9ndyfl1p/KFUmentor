// Package workflow persists application decisions and serializes browser work.
package workflow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

type Entry struct {
	Hidden           bool      `json:"hidden,omitempty"`
	ID               string    `json:"id"`
	Job              types.Job `json:"job"`
	RunID            string    `json:"runId"`
	Task             string    `json:"task"`
	Threshold        int       `json:"threshold"`
	ByScore          bool      `json:"byScore"`
	Score            int       `json:"score"`
	Recommendation   string    `json:"recommendation"`
	Reason           string    `json:"reason"`
	ReviewPoints     []string  `json:"reviewPoints,omitempty"`
	Status           string    `json:"status"`
	Stage            string    `json:"stage"`
	Error            string    `json:"error,omitempty"`
	Submission       string    `json:"submission,omitempty"` // sending/sent/without_letter/uncertain/already
	Letter           string    `json:"letter,omitempty"`
	Version          string    `json:"version,omitempty"`
	ProfileHash      string    `json:"profileHash,omitempty"`
	LetterValidation string    `json:"letterValidation,omitempty"`
	LetterWarning    string    `json:"letterWarning,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
}
type Run struct {
	Target int `json:"target"`
	Sent   int `json:"sent"`
}
type Command struct {
	ID          string `json:"id"`
	EntryID     string `json:"entryId"`
	Kind        string `json:"kind"`
	BudgetRunID string `json:"budgetRunId"`
}
type State struct {
	NextModelRequest time.Time         `json:"nextModelRequest,omitempty"`
	Entries          map[string]*Entry `json:"entries"`
	Runs             map[string]*Run   `json:"runs"`
	Queue            []Command         `json:"queue"`
	LastRun          string            `json:"lastRun"`
	ActiveRun        string            `json:"activeRun"`
}
type Store struct{ Path string }

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func JobID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || (u.Hostname() != "hh.ru" && !strings.HasSuffix(u.Hostname(), ".hh.ru")) {
		return ""
	}
	id := strings.TrimPrefix(u.Path, "/vacancy/")
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return strconv.FormatUint(n, 10)
}
func (e *Entry) CanDraft() bool {
	if e.Job.RequiresManualApplication() {
		return false
	}
	manualRecommendation := e.Recommendation == "apply" || (e.Recommendation == "review" && !e.ByScore)
	manualStatus := e.Status == "assessed" || e.Status == "review" || e.Status == "draft_error" || e.Status == "draft_ready"
	return e.Submission == "" && manualRecommendation && e.Score >= e.Threshold && manualStatus
}
func (s *Store) Update(fn func(*State) error) error { return s.access(true, fn) }
func (s *Store) access(write bool, fn func(*State) error) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	unlock, err := lockFile(s.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	state := State{Entries: map[string]*Entry{}, Runs: map[string]*Run{}}
	data, err := os.ReadFile(s.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("история повреждена: %w", err)
		}
	}
	if state.Entries == nil {
		state.Entries = map[string]*Entry{}
	}
	if state.Runs == nil {
		state.Runs = map[string]*Run{}
	}
	if err := fn(&state); err != nil {
		return err
	}
	if !write {
		return nil
	}
	data, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Path), ".applications-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.Path)
}
func (s *Store) Read() (State, error) {
	var result State
	err := s.access(false, func(st *State) error { result = *st; return nil })
	return result, err
}
func (s *Store) Recover() error {
	return s.Update(func(st *State) error {
		st.Queue = nil
		st.ActiveRun = ""
		for _, e := range st.Entries {
			if e.Submission == "sending" {
				e.Submission = "uncertain"
				e.Status = "error"
				e.Stage = "Результат отправки неизвестен"
				e.Error = "Проверьте «Мои отклики» на hh.ru. Повторная отправка заблокирована."
			}
			switch e.Status {
			case "queued_send", "queued_draft", "drafting":
				e.Status = "review"
				if e.Letter != "" {
					e.Status = "draft_ready"
				}
				e.Stage = "Задача прервана; действие нужно подтвердить заново"
			case "evaluating", "queued_retry":
				e.Status = "error"
				e.Stage = "Оценка прервана"
				e.Error = "Повторите оценку"
			}
		}
		return nil
	})
}
func (s *Store) Enqueue(id, kind, version string) error {
	return s.Update(func(st *State) error {
		e := st.Entries[id]
		if e == nil {
			return fmt.Errorf("вакансия не найдена")
		}
		for _, c := range st.Queue {
			if c.EntryID == id {
				return fmt.Errorf("действие уже в очереди")
			}
		}
		if e.Submission != "" {
			return fmt.Errorf("повторный отклик заблокирован: %s", e.Submission)
		}
		switch kind {
		case "draft":
			if !e.CanDraft() {
				return fmt.Errorf("для этой вакансии недоступен ручной отклик")
			}
			e.Status = "queued_draft"
			e.Stage = "Письмо в очереди"
			e.Error = ""
			e.Letter = ""
			e.Version = ""
			e.ProfileHash = ""
			e.LetterValidation = ""
			e.LetterWarning = ""
		case "send":
			if e.Job.RequiresManualApplication() {
				return fmt.Errorf("для этой вакансии требуется анкета работодателя")
			}
			if e.Status != "draft_ready" || version == "" || version != e.Version {
				return fmt.Errorf("сначала просмотрите актуальное письмо")
			}
			budget := e.RunID
			if st.ActiveRun != "" {
				budget = st.ActiveRun
			}
			if r := st.Runs[budget]; r != nil && r.Target > 0 && r.Sent >= r.Target {
				return fmt.Errorf("общая цель по откликам уже достигнута")
			}
			e.Status = "queued_send"
			e.Stage = "Отклик в очереди"
		case "retry":
			if e.Status != "error" && !(e.Status == "questionnaire" && e.Job.DescriptionStatus == "ready" && e.Error != "") {
				return fmt.Errorf("оценку этой вакансии нельзя повторить сейчас")
			}
			e.Status = "queued_retry"
			e.Stage = "Повторная оценка в очереди"
		default:
			return fmt.Errorf("неизвестное действие")
		}
		e.UpdatedAt = time.Now()
		budget := e.RunID
		if st.ActiveRun != "" {
			budget = st.ActiveRun
		}
		st.Queue = append(st.Queue, Command{ID: NewID(), EntryID: id, Kind: kind, BudgetRunID: budget})
		return nil
	})
}
func (s *Store) Claim() (*Command, *Entry, error) {
	var command *Command
	var entry *Entry
	err := s.Update(func(st *State) error {
		if len(st.Queue) == 0 {
			return nil
		}
		c := st.Queue[0]
		st.Queue = st.Queue[1:]
		e := st.Entries[c.EntryID]
		if e != nil && e.Submission != "" {
			return nil
		}
		if e == nil {
			return fmt.Errorf("вакансия исчезла из истории")
		}
		if e.Job.RequiresManualApplication() && c.Kind != "retry" {
			e.Status = "questionnaire"
			e.Stage = "Откройте анкету работодателя вручную"
			return nil
		}
		if c.Kind == "send" {
			if r := st.Runs[c.BudgetRunID]; r != nil && r.Target > 0 && r.Sent >= r.Target {
				e.Status = "draft_ready"
				e.Stage = "Цель достигнута; отправка отменена"
				return nil
			}
			e.Submission = "sending"
			e.Status = "applying"
			e.Stage = "Отправляю подтверждённое письмо"
		} else if c.Kind == "draft" {
			e.Status = "drafting"
			e.Stage = "Готовлю письмо для просмотра"
		} else {
			e.Status = "evaluating"
			e.Stage = "Повторно оцениваю вакансию"
		}
		command = &c
		copy := *e
		entry = &copy
		return nil
	})
	return command, entry, err
}
