package browser

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/mihnpro/Ai_agent_2/internal/prompt"
)

func (a *AIOrchestrator) searchQueries(task string) []string {
	data, _ := json.Marshal(map[string]string{"task": task})
	response, err := a.llmClient.GenerateJSON(string(data), prompt.SearchSystem)
	if err == nil {
		var result struct {
			Queries []string `json:"queries"`
		}
		if err = json.Unmarshal([]byte(response), &result); err == nil {
			if queries := cleanSearchQueries(result.Queries); len(queries) > 0 {
				log.Printf("🔍 Поисковые запросы модели: %s", strings.Join(queries, " | "))
				return queries
			}
			err = fmt.Errorf("модель не вернула короткие поисковые запросы")
		}
	}
	log.Printf("⚠️ Не удалось разобрать задачу через модель: %v. Использую локальный разбор", err)
	return fallbackSearchQueries(task)
}

func cleanSearchQueries(queries []string) []string {
	var result []string
	seen := map[string]bool{}
	for _, query := range queries {
		query = strings.Join(strings.Fields(query), " ")
		key := strings.ToLower(query)
		if query == "" || len([]rune(query)) > 100 || len(strings.Fields(query)) > 8 ||
			strings.ContainsAny(query, "\n\r{}<>") || strings.Contains(query, "://") || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, query)
		if len(result) == 3 {
			break
		}
	}
	return result
}

// Word boundaries use Unicode letters so cleaning never damages .NET, C++,
// hh-like substrings inside technology names, or version numbers.
func fallbackSearchQueries(task string) []string {
	lower := strings.ToLower(task)
	if regexp.MustCompile(`машинн\p{L}*\s+обуч\p{L}*|machine\s+learning|(^|[^\p{L}])ml([^\p{L}]|$)`).MatchString(lower) {
		level := ""
		for _, candidate := range []string{"junior", "middle", "senior"} {
			if regexp.MustCompile(`(^|[^\p{L}])` + candidate + `([^\p{L}]|$)`).MatchString(lower) {
				level = candidate + " "
				break
			}
		}
		if strings.Contains(lower, "джуниор") {
			level = "junior "
		}
		return []string{level + "ML Engineer", level + "Machine Learning Engineer", level + "машинное обучение"}
	}
	query := regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])(hh\.ru|headhunter)([^\p{L}\p{N}]|$)`).ReplaceAllString(task, " ")
	query = regexp.MustCompile(`(?i)\d+\s+(подходящ\p{L}*\s+)?ваканси\p{L}*`).ReplaceAllString(query, " ")
	stop := map[string]bool{}
	for _, word := range strings.Fields("я мне хочу найти найди ищу ищи поищи работу работы работа вакансии вакансий вакансию вакансия подходящие подходящих подходящую пожалуйста несколько с со по в на для сфере области") {
		stop[word] = true
	}
	var words []string
	for _, word := range strings.Fields(query) {
		word = strings.Trim(word, ",!?;:\"«»")
		if word == "" || stop[strings.ToLower(word)] {
			continue
		}
		if len(words) > 0 && strings.EqualFold(words[len(words)-1], word) {
			continue
		}
		words = append(words, word)
	}
	query = strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		return nil
	}
	return []string{query}
}
