package browser

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

func normalizedFact(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

var optionalRequirement = regexp.MustCompile(`(?i)(преимуществ|желательн|будет плюсом|необязательн|не обязател)`)
var explicitExperienceFact = regexp.MustCompile(`(?i)(\d.{0,25}(год|лет|месяц)|20\d\d|(стажа|опыта).{0,20}нет|нет.{0,20}(стажа|опыта)|без.{0,15}опыта|не работал)`)
var requirementHeading = regexp.MustCompile(`(?i)^(требования|что мы жд[её]м от кандидата|кого мы ищем|что важно|ожидания)(:|$)`)
var otherHeading = regexp.MustCompile(`(?i)^(обязанности|чем предстоит заниматься|мы предлагаем|условия|задачи)(:|$)`)

func requirementsSection(text string) (string, bool) {
	var result []string
	found, inside := false, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if requirementHeading.MatchString(line) {
			found = true
			inside = true
			continue
		}
		if otherHeading.MatchString(line) {
			inside = false
			continue
		}
		if inside {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n"), found
}

func validateEvaluationFacts(e *JobEvaluationResult, job types.Job, profile types.Resume, task string, context types.SearchContext) error {
	if err := validateEvaluation(e); err != nil {
		return err
	}
	if e.RoleMatch != "direct" && e.RoleMatch != "related" && e.RoleMatch != "different" {
		return fmt.Errorf("нужно явно указать role_match: direct, related или different")
	}
	if len(e.Gaps) > 3 {
		return fmt.Errorf("mandatory_gaps: не более трёх главных обязательных пробелов")
	}
	vacancyFacts := normalizedFact(strings.Join([]string{job.Title, job.Snippet, job.ExperienceRequirement, job.WorkFormat, job.Employment, job.Location}, "\n"))
	candidateFacts := normalizedFact(fmt.Sprint(profile) + "\n" + task)
	requirements, hasRequirements := requirementsSection(job.Snippet)
	for _, gap := range e.Gaps {
		switch gap.Kind {
		case "skill", "experience", "education", "location", "work_format", "other":
		default:
			return fmt.Errorf("некорректный kind обязательного пробела")
		}
		quote := normalizedFact(gap.VacancyQuote)
		if len([]rune(quote)) < 8 || !strings.Contains(vacancyFacts, quote) {
			return fmt.Errorf("обязательный пробел не подтверждён точной цитатой из вакансии")
		}
		if optionalRequirement.MatchString(quote) {
			return fmt.Errorf("желательный навык или преимущество нельзя считать обязательным пробелом")
		}
		// Check the sentence as well: quoting just the skill must not strip 'optional'.
		for _, line := range strings.FieldsFunc(job.Snippet, func(r rune) bool { return r == '\n' || r == ';' || r == '.' }) {
			if strings.Contains(normalizedFact(line), quote) && optionalRequirement.MatchString(line) {
				return fmt.Errorf("цитата взята из желательного условия, а не обязательного требования")
			}
		}
		if hasRequirements && (gap.Kind == "skill" || gap.Kind == "experience" || gap.Kind == "education") && !strings.Contains(normalizedFact(requirements+"\n"+job.ExperienceRequirement), quote) {
			return fmt.Errorf("обязанность или условие предложения нельзя превращать в требование к предыдущему опыту")
		}
		if gap.Kind == "experience" && strings.Contains(normalizedFact(job.ExperienceRequirement), "не требуется") {
			return fmt.Errorf("шапка говорит «опыт не требуется»: нельзя отказывать из-за неподтверждённого стажа")
		}
		if gap.Kind == "location" && context.AreaName != "" && strings.Contains(normalizedFact(job.Location+" "+gap.VacancyQuote), normalizedFact(context.AreaName)) {
			return fmt.Errorf("город вакансии совпадает с выбранным: это не препятствие")
		}
		if gap.Status != "unknown" && gap.Status != "conflict" {
			return fmt.Errorf("status пробела должен быть unknown или conflict")
		}
		if (gap.Kind == "location" || gap.Kind == "work_format") && gap.Status == "unknown" {
			return fmt.Errorf("неуказанные предпочтения по месту и формату работы не являются обязательным пробелом")
		}
		if gap.Status == "conflict" {
			evidence := normalizedFact(gap.CandidateQuote)
			if len([]rune(evidence)) < 8 || !strings.Contains(candidateFacts, evidence) {
				return fmt.Errorf("противоречие не подтверждено цитатой из резюме или задачи; отсутствие сведений означает unknown")
			}
			if gap.Kind == "experience" && !explicitExperienceFact.MatchString(gap.CandidateQuote) {
				return fmt.Errorf("junior или начинающий не доказывает отсутствие стажа; используйте unknown")
			}
		}
	}
	// Model scores and recommendations are advisory when verified findings place
	// the vacancy in an explicit review/skip band. Normalize those below rather
	// than failing an otherwise factual assessment.
	if e.RoleMatch == "direct" && len(e.Gaps) == 0 && (e.Recommendation != "apply" || e.MatchScore < 7) {
		return fmt.Errorf("отказ или низкий балл без обязательного пробела и без иной профессии не обоснован")
	}

	return nil
}

// The final score is an ordinal decision band, not a hiring probability.
// Borderline decisions use fixed bands. Strong direct matches keep 7–10 so
// existing user thresholds of 9 and 10 remain meaningful.
func interpretEvaluation(e *JobEvaluationResult, job types.Job, profile types.Resume) {
	e.ModelScore = e.MatchScore
	conflicts := 0
	conflictQuote := ""
	criticalUnknown := false
	for _, gap := range e.Gaps {
		if gap.Status == "conflict" {
			conflicts++
			conflictQuote = gap.VacancyQuote
		}
		if gap.Status == "unknown" && (gap.Kind == "experience" || gap.Kind == "education") {
			criticalUnknown = true
		}
	}
	switch {
	case e.RoleMatch == "different":
		e.MatchScore = 2
		e.Recommendation = "skip"
		e.Reason = "Другое профессиональное направление по сравнению с запросом."
	case conflicts > 0:
		e.MatchScore = 3
		e.Recommendation = "skip"
		e.Reason = "Подтверждено противоречие обязательному требованию: «" + conflictQuote + "»."
	case e.RoleMatch == "related":
		e.MatchScore = 6
		if len(e.Gaps) > 0 {
			e.MatchScore = 5
		}
		e.Recommendation = "review"
		e.Reason = "Смежное направление: отличается характер задач."
	case len(e.Gaps) > 0:
		e.MatchScore = 6
		if len(e.Gaps) > 1 || criticalUnknown {
			e.MatchScore = 5
		}
		e.Recommendation = "review"
		e.Reason = "Нужно уточнить обязательное требование: «" + e.Gaps[0].VacancyQuote + "»."
	default:
		e.MatchScore = e.ModelScore
		e.Recommendation = "apply"
		e.Reason = "Направление соответствует запросу, обязательных пробелов и противоречий не выявлено."
	}
	e.Analysis = e.Reason
	if strings.Contains(normalizedFact(job.ExperienceRequirement), "не требуется") {
		e.Analysis += " В шапке вакансии указано: опыт не требуется."
	}
	e.ReviewPoints = nil
	if e.Recommendation == "review" {
		if e.RoleMatch == "related" {
			e.ReviewPoints = append(e.ReviewPoints, "Смежная роль: сравните обязанности с желаемым направлением.")
		}
		for _, gap := range e.Gaps {
			if len(e.ReviewPoints) >= 2 {
				break
			}
			e.ReviewPoints = append(e.ReviewPoints, "Не подтверждено обязательное требование: «"+gap.VacancyQuote+"».")
		}
		question := "Подтвердите, выполняете ли вы указанные обязательные требования."
		if e.RoleMatch == "related" {
			question = "Рассматриваете ли вы такую смежную работу?"
			if len(e.Gaps) > 0 {
				question = "Подтвердите требования выше и уточните, рассматриваете ли вы такую смежную работу."
			}
		}
		e.ReviewPoints = append(e.ReviewPoints, question)
	}
	// A paraphrased skill must not create a new fact about the candidate.
	facts := normalizedFact(fmt.Sprint(profile))
	var confirmed []string
	for _, skill := range e.SkillsMatch {
		if normalizedFact(skill) != "" && strings.Contains(facts, normalizedFact(skill)) {
			confirmed = append(confirmed, skill)
		}
	}
	e.SkillsMatch = confirmed
}
