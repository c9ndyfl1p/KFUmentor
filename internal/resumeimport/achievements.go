package resumeimport

import (
	"regexp"
	"strings"
)

// Preserve an explicitly labelled source section when the model omits it. A
// participant without a prize is still a fact the user chose to include.
func achievementSectionText(text string) string {
	heading := regexp.MustCompile(`(?i)^\s*(?:личные\s+достижения|достижения|награды(?:\s+и\s+достижения)?|achievements|awards(?:\s+and\s+achievements)?)\s*[:：—-]?\s*(.*)$`)
	nextSection := regexp.MustCompile(`(?i)^\s*(?:образование|опыт\s+работы|опыт|навыки|ключевые\s+навыки|проекты|учебные\s+проекты|личные\s+проекты|о\s+себе|контакты|языки|курсы|дополнительное\s+образование|education|experience|skills|projects|summary|contacts?|languages|courses)\s*(?:[:：].*|$)`)
	var lines []string
	found := false
	for _, line := range strings.Split(text, "\n") {
		if !found {
			if match := heading.FindStringSubmatch(line); len(match) == 2 {
				found = true
				if value := strings.TrimSpace(match[1]); value != "" {
					lines = append(lines, value)
				}
			}
			continue
		}
		if nextSection.MatchString(line) {
			break
		}
		if value := strings.TrimSpace(line); value != "" {
			lines = append(lines, value)
		}
	}
	section := strings.Join(lines, "\n")
	if len([]rune(section)) > 12000 {
		return ""
	}
	return section
}
