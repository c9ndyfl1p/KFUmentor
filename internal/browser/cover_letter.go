package browser

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/prompt"
	"github.com/mihnpro/Ai_agent_2/internal/types"
)

type letterExample struct {
	Vacancy string `json:"vacancy"`
	Letter  string `json:"letter"`
}

// An optional local file provides style examples without changing model weights.
func loadLetterExamples(path string) ([]letterExample, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<10 {
		return nil, fmt.Errorf("файл примеров превышает 64 КБ")
	}
	var examples []letterExample
	if err := json.Unmarshal(data, &examples); err != nil {
		return nil, fmt.Errorf("некорректный JSON примеров: %w", err)
	}
	if len(examples) > 5 {
		return nil, fmt.Errorf("оставьте не более 5 примеров писем")
	}
	for _, example := range examples {
		if strings.TrimSpace(example.Letter) == "" || len([]rune(example.Letter)) > 3000 || len([]rune(example.Vacancy)) > 1500 {
			return nil, fmt.Errorf("пример должен содержать письмо до 3000 символов и описание вакансии до 1500 символов")
		}
	}
	return examples, nil
}

func (a *AIOrchestrator) generateCoverLetter(title, company string, job types.Job) (string, error) {
	if strings.TrimSpace(a.resume.Summary) == "" && len(a.resume.Skills) == 0 && len(a.resume.Experience) == 0 {
		return "", fmt.Errorf("сначала заполните и сохраните профиль кандидата в панели")
	}
	detailed, err := a.loadJobDetails(job)
	if err != nil {
		return "", err
	}
	examples, err := loadLetterExamples(a.letterExamplesPath)
	if err != nil {
		return "", err
	}
	// Use full experience and summary, but do not send contact details to the model.
	profile := a.resume
	profile.Summary = cleanProfileText(profile.Summary)
	profile.Skills = cleanProfileSkills(profile.Skills)
	profile.ContactInfo = types.Contact{}
	data, err := json.Marshal(struct {
		Resume   types.Resume    `json:"resume"`
		Vacancy  types.Job       `json:"vacancy"`
		Examples []letterExample `json:"style_examples"`
	}{profile, detailed, examples})
	if err != nil {
		return "", err
	}
	input := string(data)
	var unsupportedClaims []string
	for attempt := 0; attempt < 2; attempt++ {
		letter, err := a.llmClient.GenerateWithBudget(input, prompt.LetterSystem, 600, 90*time.Second)
		if err != nil {
			return "", err
		}
		letter, err = cleanCoverLetter(letter)
		if err != nil {
			return "", err
		}
		review, err := a.reviewCoverLetter(profile, letter)
		if err != nil {
			return "", &letterCheckFailure{Err: err}
		}
		if review.Supported != nil && *review.Supported && len(review.UnsupportedClaims) == 0 {
			return letter, nil
		}
		unsupportedClaims = append([]string(nil), review.UnsupportedClaims...)
		log.Printf("⚠️ Проверка письма: неподтверждённых утверждений — %d; исправляю текст", len(review.UnsupportedClaims))
		correction, _ := json.Marshal(map[string]interface{}{
			"original_data":             json.RawMessage(data),
			"previous_letter":           letter,
			"remove_unsupported_claims": review.UnsupportedClaims,
			"instruction":               "Перепиши письмо. Удали все указанные неподтверждённые утверждения. Навыки перечисляй отдельно от проектов, если в резюме не сказано, что они использовались в проекте. Не заменяй удалённые утверждения новыми предположениями.",
		})
		input = string(correction)
	}
	return "", &letterCheckFailure{Err: fmt.Errorf("после исправления остались неподтверждённые утверждения: %s", strings.Join(unsupportedClaims, "; "))}
}

func cleanCoverLetter(letter string) (string, error) {
	// Some local models emit reasoning in their text field. Never send it as a letter.
	letter = regexp.MustCompile(`(?s)<think>.*?</think>`).ReplaceAllString(letter, "")
	letter = strings.TrimSpace(letter)
	for _, prefix := range []string{"Сопроводительное письмо:", "Письмо:"} {
		letter = strings.TrimSpace(strings.TrimPrefix(letter, prefix))
	}
	if len([]rune(letter)) < 40 || len([]rune(letter)) > 3000 || strings.Contains(letter, "<think>") || strings.Contains(letter, "```") {
		return "", fmt.Errorf("модель вернула пустое, слишком длинное письмо или служебный текст; отклик не отправлен")
	}
	return letter, nil
}

type letterReview struct {
	Supported         *bool    `json:"supported"`
	UnsupportedClaims []string `json:"unsupported_claims"`
}

// Only a failed review can fall back to a factual draft for explicit human review.
// Generation, profile and vacancy errors keep their ordinary blocking behavior.
type letterCheckFailure struct{ Err error }

func (e *letterCheckFailure) Error() string { return e.Err.Error() }
func (e *letterCheckFailure) Unwrap() error { return e.Err }

func (a *AIOrchestrator) reviewCoverLetter(profile types.Resume, letter string) (letterReview, error) {
	profile.ContactInfo = types.Contact{}
	data, err := json.Marshal(struct {
		Resume types.Resume `json:"resume"`
		Letter string       `json:"letter"`
	}{profile, letter})
	if err != nil {
		return letterReview{}, err
	}
	system := prompt.LetterReviewSystem
	var formatErr error
	for attempt := 0; attempt < 2; attempt++ {
		response, err := a.llmClient.GenerateJSONWithBudget(string(data), system, 512, 60*time.Second)
		if err != nil {
			return letterReview{}, fmt.Errorf("проверка фактов письма: %w", err)
		}
		review, parseErr := parseLetterReview(response)
		if parseErr == nil {
			return review, nil
		}
		formatErr = parseErr
		system = prompt.LetterReviewSystem + "\nПредыдущий ответ не прошёл проверку формата. Верни ровно JSON с supported (boolean) и unsupported_claims (массив строк). Не меняй правила проверки фактов."
	}
	return letterReview{}, fmt.Errorf("проверка фактов письма не выполнена после двух попыток: %w; подготовьте письмо заново", formatErr)
}

func parseLetterReview(response string) (letterReview, error) {
	var review letterReview
	if err := json.Unmarshal([]byte(extractJSONObject(response)), &review); err != nil {
		return review, fmt.Errorf("модель вернула некорректный JSON")
	}
	if review.Supported == nil || review.UnsupportedClaims == nil {
		return review, fmt.Errorf("отсутствуют supported или unsupported_claims")
	}
	for _, claim := range review.UnsupportedClaims {
		if strings.TrimSpace(claim) == "" {
			return review, fmt.Errorf("модель указала пустое неподтверждённое утверждение")
		}
	}
	if *review.Supported != (len(review.UnsupportedClaims) == 0) {
		return review, fmt.Errorf("противоречивый результат проверки фактов")
	}
	return review, nil
}
