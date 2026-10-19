package workflow

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Path: filepath.Join(t.TempDir(), "history.json")}
	if err := s.Update(func(st *State) error {
		st.Entries["1"] = &Entry{ID: "1", Job: types.Job{Title: "Analyst", URL: "https://hh.ru/vacancy/1"}, RunID: "run", Score: 7, Threshold: 6, Recommendation: "review", Status: "review"}
		st.Runs["run"] = &Run{Target: 2}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestDraftNeverAuthorizesSendAndDoubleClick(t *testing.T) {
	s := testStore(t)
	if err := s.Enqueue("1", "send", ""); err == nil {
		t.Fatal("send before preview")
	}
	if err := s.Enqueue("1", "draft", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue("1", "draft", ""); err == nil {
		t.Fatal("duplicate draft")
	}
	command, e, err := s.Claim()
	if err != nil || command.Kind != "draft" || e.Submission != "" {
		t.Fatal(command, e, err)
	}
	s.Update(func(st *State) error {
		e := st.Entries["1"]
		e.Status = "draft_ready"
		e.Version = "v1"
		e.Letter = "Approved text"
		return nil
	})
	if err := s.Enqueue("1", "send", "old"); err == nil {
		t.Fatal("stale preview accepted")
	}
	if err := s.Enqueue("1", "send", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue("1", "send", "v1"); err == nil {
		t.Fatal("double send queued")
	}
	c, entry, err := s.Claim()
	if err != nil || entry.Submission != "sending" || c.BudgetRunID != "run" {
		t.Fatal(c, entry, err)
	}
	if err := s.Enqueue("1", "send", "v1"); err == nil {
		t.Fatal("in-flight send repeated")
	}
}
func TestRestartKeepsDraftsButNeverReplaysSend(t *testing.T) {
	s := testStore(t)
	s.Update(func(st *State) error {
		e := st.Entries["1"]
		e.Letter = "Keep me"
		e.Version = "v"
		e.Status = "draft_ready"
		return nil
	})
	if err := s.Enqueue("1", "send", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Read()
	if len(st.Queue) != 0 || st.Entries["1"].Status != "draft_ready" || st.Entries["1"].Letter != "Keep me" {
		t.Fatal(st)
	}
	s.Enqueue("1", "send", "v")
	s.Claim()
	s.Recover()
	st, _ = s.Read()
	if st.Entries["1"].Submission != "uncertain" {
		t.Fatal(st.Entries["1"])
	}
	if err := s.Enqueue("1", "send", "v"); err == nil {
		t.Fatal("uncertain send replayed")
	}
}
func TestCommonBudgetAndReviewThreshold(t *testing.T) {
	s := testStore(t)
	s.Update(func(st *State) error {
		st.ActiveRun = "current"
		st.Runs["current"] = &Run{Target: 1}
		e := st.Entries["1"]
		e.Status = "draft_ready"
		e.Version = "v"
		return nil
	})
	s.Enqueue("1", "send", "v")
	s.Update(func(st *State) error { st.Runs["current"].Sent = 1; return nil })
	c, _, err := s.Claim()
	if err != nil || c != nil {
		t.Fatal("goal exceeded", c, err)
	}
	st, _ := s.Read()
	if st.Entries["1"].Status != "draft_ready" {
		t.Fatal(st.Entries["1"])
	}
	e := Entry{Score: 5, Threshold: 6, Status: "review", Recommendation: "review"}
	if e.CanDraft() {
		t.Fatal("review below threshold got manual action")
	}
}
func TestConcurrentUpdatesDoNotLoseHistory(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Update(func(st *State) error { st.Runs["run"].Sent++; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	st, err := s.Read()
	if err != nil || st.Runs["run"].Sent != 15 {
		t.Fatal(st, err)
	}
}

func TestDraftRetryClearsRejectedPreview(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "history.json")}
	store.Update(func(st *State) error {
		st.Entries["1"] = &Entry{ID: "1", Status: "draft_error", Recommendation: "review", Score: 7, Threshold: 6, Error: "old error", Letter: "old letter", Version: "old version", ProfileHash: "old profile", LetterValidation: "old check"}
		return nil
	})
	if err := store.Enqueue("1", "draft", ""); err != nil {
		t.Fatal(err)
	}
	st, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	e := st.Entries["1"]
	if e.Status != "queued_draft" || e.Error != "" || e.Letter != "" || e.Version != "" || e.ProfileHash != "" || e.LetterValidation != "" {
		t.Fatal(e)
	}
	if err := store.Enqueue("1", "send", "old version"); err == nil {
		t.Fatal("old version send allowed")
	}
}
