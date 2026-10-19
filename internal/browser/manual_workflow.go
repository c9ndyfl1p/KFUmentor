package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/types"
	"github.com/mihnpro/Ai_agent_2/internal/workflow"
)

type ApplicationFailure struct {
	Err             error
	Sent, Uncertain bool
}

func (e *ApplicationFailure) Error() string { return e.Err.Error() }
func (e *ApplicationFailure) Unwrap() error { return e.Err }
func currentStore() *workflow.Store {
	p := os.Getenv("KFUMENTOR_WORKFLOW_PATH")
	if p == "" {
		return nil
	}
	return &workflow.Store{Path: p}
}
func (a *AIOrchestrator) rememberJob(job types.Job, evaluation *JobEvaluationResult, evalErr error, starting bool) error {
	store := currentStore()
	if store == nil {
		return nil
	}
	id := workflow.JobID(job.URL)
	if id == "" {
		return fmt.Errorf("некорректная ссылка на вакансию")
	}
	return store.Update(func(st *workflow.State) error {
		e := st.Entries[id]
		if e != nil && (e.Submission != "" || e.Status == "queued_draft" || e.Status == "queued_send" || e.Status == "drafting" || e.Status == "draft_ready") {
			return nil
		}
		if e == nil {
			e = &workflow.Entry{ID: id}
			st.Entries[id] = e
		}
		e.Hidden = false
		e.Job = job
		e.RunID = os.Getenv("KFUMENTOR_RUN_ID")
		e.Task = a.currentTask
		e.Threshold = a.minimumScore
		e.ByScore = a.applyByScore
		e.UpdatedAt = time.Now()
		if starting {
			e.Status = "evaluating"
			e.Stage = "Оцениваю вакансию"
			e.Error = ""
			return nil
		}
		if evalErr != nil {
			e.Status = "error"
			e.Stage = "Ошибка оценки"
			e.Error = evalErr.Error()
			e.Score = 0
			return nil
		}
		if evaluation == nil {
			return fmt.Errorf("модель не вернула оценку")
		}
		e.Score = evaluation.MatchScore
		e.Recommendation = evaluation.Recommendation
		e.Reason = evaluation.Reason
		e.ReviewPoints = evaluation.ReviewPoints
		e.Error = ""
		e.Letter = ""
		e.Version = ""
		e.Status = "assessed"
		e.Stage = "Оценка завершена"
		if e.Recommendation == "review" {
			e.Status = "review"
			e.Stage = "Нужна ручная проверка"
		} else if e.Recommendation == "skip" {
			e.Status = "skipped"
			e.Stage = "Модель рекомендует пропустить"
		}
		return nil
	})
}
func (a *AIOrchestrator) evaluateTracked(job types.Job) (*JobEvaluationResult, error) {
	if err := a.rememberJob(job, nil, nil, true); err != nil {
		return nil, err
	}
	evaluation, err := a.evaluateJobForTaskWithAnalysis(job, a.currentTask)
	if saveErr := a.rememberJob(job, evaluation, err, false); saveErr != nil {
		return nil, saveErr
	}
	return evaluation, err
}
func (a *AIOrchestrator) reserveSubmission(job types.Job) error {
	store := currentStore()
	if store == nil {
		return nil
	}
	return store.Update(func(st *workflow.State) error {
		e := st.Entries[workflow.JobID(job.URL)]
		if e == nil {
			return fmt.Errorf("вакансия не сохранена в истории")
		}
		if e.Submission != "" || e.Status == "queued_draft" || e.Status == "queued_send" || e.Status == "draft_ready" || e.Status == "drafting" {
			return errAlreadyApplied
		}
		if r := st.Runs[os.Getenv("KFUMENTOR_RUN_ID")]; r != nil && r.Target > 0 && r.Sent >= r.Target {
			return fmt.Errorf("цель по откликам уже достигнута")
		}
		e.Submission = "sending"
		e.Status = "applying"
		e.Stage = "Готовлю и отправляю отклик"
		return nil
	})
}
func (a *AIOrchestrator) finishSubmission(job types.Job, runID, result string, sendErr error) error {
	store := currentStore()
	if store == nil {
		return nil
	}
	return store.Update(func(st *workflow.State) error {
		e := st.Entries[workflow.JobID(job.URL)]
		if e == nil {
			return fmt.Errorf("нет записи отправки")
		}
		if e.Submission != "sending" {
			return nil
		}
		e.UpdatedAt = time.Now()
		e.Error = ""
		var failure *ApplicationFailure
		switch {
		case errors.Is(sendErr, errAlreadyApplied):
			e.Submission = "already"
			e.Status = "skipped"
			e.Stage = "Отклик уже существует"
		case errors.As(sendErr, &failure) && failure.Sent:
			e.Submission = "without_letter"
			e.Status = "warning"
			e.Stage = "Отклик отправлен без письма"
			e.Error = sendErr.Error()
		case sendErr == nil:
			e.Submission = "sent"
			e.Status = "success"
			e.Stage = "Отклик и письмо отправлены"
		default:
			e.Status = "error"
			e.Stage = "Не удалось отправить отклик"
			e.Error = sendErr.Error()
			if failure != nil && !failure.Uncertain {
				e.Submission = ""
				e.Status = "draft_error"
			} else {
				e.Submission = "uncertain"
				e.Stage = "Результат отправки неизвестен; проверьте hh.ru"
			}
		}
		if e.Submission == "sent" || e.Submission == "without_letter" {
			if r := st.Runs[runID]; r != nil {
				r.Sent++
			}
		}
		return nil
	})
}
func (a *AIOrchestrator) submitTracked(job types.Job) (string, error) {
	if err := a.reserveSubmission(job); err != nil {
		return "", err
	}
	result, err := a.performJobApplication(job)
	if saveErr := a.finishSubmission(job, os.Getenv("KFUMENTOR_RUN_ID"), result, err); saveErr != nil {
		return "", saveErr
	}
	var failure *ApplicationFailure
	if errors.As(err, &failure) && failure.Sent {
		log.Printf("⚠️ Отклик отправлен без письма: %v", err)
		return "Отклик отправлен без письма; проверьте чат вакансии", nil
	}
	return result, err
}
func (a *AIOrchestrator) totalSent() int {
	store := currentStore()
	if store == nil {
		return 0
	}
	st, err := store.Read()
	if err != nil {
		return 0
	}
	if r := st.Runs[os.Getenv("KFUMENTOR_RUN_ID")]; r != nil {
		return r.Sent
	}
	return 0
}
func (a *AIOrchestrator) drainManualQueue() error {
	store := currentStore()
	if store == nil {
		return nil
	}
	for i := 0; i < 10; i++ {
		command, e, err := store.Claim()
		if err != nil {
			return err
		}
		if command == nil {
			return nil
		}
		log.Printf("🧑 Ручное действие: %s — %s", command.Kind, e.Job.Title)
		originalTask := a.currentTask
		a.currentTask = e.Task
		switch command.Kind {
		case "retry":
			// Enqueueing a retry never authorizes a new automatic application.
			a.jobDetails = map[string]types.Job{}
			evaluation, evalErr := a.evaluateJobForTaskWithAnalysis(e.Job, e.Task)
			err = store.Update(func(st *workflow.State) error {
				item := st.Entries[e.ID]
				item.UpdatedAt = time.Now()
				item.Error = ""
				if evalErr != nil {
					item.Status = "error"
					item.Stage = "Ошибка повторной оценки"
					item.Error = evalErr.Error()
					return nil
				}
				item.Score = evaluation.MatchScore
				item.Recommendation = evaluation.Recommendation
				item.Reason = evaluation.Reason
				item.ReviewPoints = evaluation.ReviewPoints
				item.Status = "assessed"
				item.Stage = "Повторная оценка завершена"
				if item.Recommendation == "review" {
					item.Status = "review"
					item.Stage = "Нужна ручная проверка"
				}
				if item.Recommendation == "skip" {
					item.Status = "skipped"
				}
				return nil
			})
		case "draft":
			var letter string
			var warning string
			// Always use the latest saved profile. Capture its hash for confirmation.
			profile, digest, digestErr := readManualProfile(a.resumePath)
			a.resume = profile
			var draftErr error
			if digestErr != nil {
				draftErr = digestErr
			} else {
				letter, draftErr = a.generateCoverLetter(e.Job.Title, e.Job.Company, e.Job)
				var checkFailure *letterCheckFailure
				if errors.As(draftErr, &checkFailure) {
					// Do not expose the unchecked generated claims as an approved draft.
					// Build a short letter from the saved facts for the user's own review.
					detailed, _ := a.cachedJobDetails(e.Job.URL)
					letter = buildCoverLetterFallback(detailed.Title, detailed.Company, detailed.Snippet, cleanProfileSkills(profile.Skills), cleanProfileText(profile.Summary))
					letter, draftErr = cleanCoverLetter(letter)
					warning = "Письмо не прошло автоматическую проверку фактов. Подготовлен короткий запасной вариант только по сведениям профиля. Проверьте текст перед отправкой."
				}
			}
			err = store.Update(func(st *workflow.State) error {
				item := st.Entries[e.ID]
				item.UpdatedAt = time.Now()
				if draftErr != nil {
					item.Status = "draft_error"
					item.Stage = "Письмо не подготовлено"
					item.Error = draftErr.Error()
				} else {
					item.Status = "draft_ready"
					item.Stage = "Проверьте письмо и нажмите «Отправить»"
					item.Letter = letter
					item.Version = workflow.NewID()
					item.ProfileHash = digest
					item.LetterValidation = letterValidation(digest, letter)
					item.LetterWarning = warning
					item.Error = ""
				}
				return nil
			})
		case "send":
			profile, digest, digestErr := readManualProfile(a.resumePath)
			var sendErr error
			var result string
			if digestErr != nil || digest != e.ProfileHash {
				sendErr = &ApplicationFailure{Err: fmt.Errorf("профиль изменился; подготовьте и проверьте письмо заново")}
			} else {
				a.resume = profile
				a.approvedLetter = e.Letter
				a.approvedLetterVerified = e.LetterValidation != "" && e.LetterValidation == letterValidation(digest, e.Letter)
				result, sendErr = a.performJobApplication(e.Job)
				a.approvedLetter = ""
				a.approvedLetterVerified = false
			}
			err = a.finishSubmission(e.Job, command.BudgetRunID, result, sendErr)
			var failure *ApplicationFailure
			if err == nil && errors.As(sendErr, &failure) && failure.Uncertain && !failure.Sent {
				err = sendErr
			}
		}
		a.currentTask = originalTask
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *AIOrchestrator) workflowTotalCounter() func() int {
	if currentStore() == nil {
		return nil
	}
	return a.totalSent
}

func readManualProfile(path string) (types.Resume, string, error) {
	var profile types.Resume
	b, err := os.ReadFile(path)
	if err != nil {
		return profile, "", err
	}
	if err := json.Unmarshal(b, &profile); err != nil {
		return profile, "", fmt.Errorf("профиль повреждён: %w", err)
	}
	sum := sha256.Sum256(b)
	return profile, hex.EncodeToString(sum[:]), nil
}

// Bind the prepared draft to the exact displayed letter and profile bytes.
// A fallback with a warning requires the same explicit human send confirmation.
// A changed letter/profile or an older draft without this marker is rechecked.
func letterValidation(profileHash, letter string) string {
	sum := sha256.Sum256([]byte("letter-validation-v1\x00" + profileHash + "\x00" + letter))
	return hex.EncodeToString(sum[:])
}
