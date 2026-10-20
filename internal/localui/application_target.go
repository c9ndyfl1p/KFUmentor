package localui

import (
	"fmt"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

func normalizeApplicationTarget(input *taskInput) error {
	if input.ApplicationTarget < 0 || input.ApplicationTarget > types.MaxApplicationTarget {
		return fmt.Errorf("количество откликов должно быть от 1 до %d", types.MaxApplicationTarget)
	}
	if input.ApplicationTarget == 0 {
		input.ApplicationTarget = types.DefaultApplicationTarget
	}
	return nil
}

func normalizeVacancyLimit(input *taskInput) error {
	if input.VacancyLimit == 0 {
		input.VacancyLimit = types.DefaultVacancyLimit
	}
	if input.VacancyLimit < 1 || input.VacancyLimit > types.MaxVacancyLimit {
		return fmt.Errorf("лимит поиска вакансий должен быть от 1 до %d", types.MaxVacancyLimit)
	}
	return nil
}

func normalizeMinimumScore(input *taskInput) error {
	if input.MinimumScore < 0 || input.MinimumScore > types.MaxMatchScore {
		return fmt.Errorf("минимальная оценка должна быть от 0 до %d", types.MaxMatchScore)
	}
	return nil
}

func (s *server) taskArguments(input taskInput) []string {
	vacancyLimit := input.VacancyLimit
	if vacancyLimit == 0 {
		vacancyLimit = types.DefaultVacancyLimit
	}
	applicationTarget := input.ApplicationTarget
	if applicationTarget == 0 {
		applicationTarget = types.DefaultApplicationTarget
	}
	minimumScore := input.MinimumScore
	args := []string{
		"--task=" + input.Task,
		"--headless=" + fmt.Sprint(!input.Visible),
		"--auto-apply=" + fmt.Sprint(input.AutoApply),
		"--apply-by-score=" + fmt.Sprint(input.ApplyByScore),
		"--application-target=" + fmt.Sprint(applicationTarget),
		"--vacancy-limit=" + fmt.Sprint(vacancyLimit),
		"--min-score=" + fmt.Sprint(minimumScore),
		"--resume=" + localResumePath,
		"--profile=" + s.cfg.ProfileDir,
		"--model-url=" + s.cfg.ModelURL,
		"--model=" + s.cfg.ModelName,
		"--debug=" + fmt.Sprint(s.cfg.Debug),
	}
	if input.AreaID > 0 {
		args = append(args, "--area="+fmt.Sprint(input.AreaID), "--area-name="+input.AreaName)
	}
	return args
}
