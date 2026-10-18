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

func (s *server) taskArguments(input taskInput) []string {
	args := []string{
		"--task=" + input.Task,
		"--headless=" + fmt.Sprint(!input.Visible),
		"--auto-apply=" + fmt.Sprint(input.AutoApply),
		"--application-target=" + fmt.Sprint(input.ApplicationTarget),
		"--resume=" + localResumePath,
		"--profile=" + s.cfg.ProfileDir,
		"--model-url=" + s.cfg.ModelURL,
		"--model=" + s.cfg.ModelName,
		"--debug=" + fmt.Sprint(s.cfg.Debug),
	}
	if input.AreaID > 0 {
		args = append(args, "--area="+fmt.Sprint(input.AreaID))
	}
	return args
}
