package localui

import (
	"strings"
	"testing"
)

func TestAutoApplyProgressSplitOutput(t *testing.T) {
	s := &server{}
	w := &outputWriter{server: s}
	line := "2026/10/18 23:00:00 🤔 Анализирую вакансию 1/5: ML инженер | URL: https://hh.ru/vacancy/123\n"
	raw := []byte(line)
	for _, b := range raw {
		w.Write([]byte{b})
	}
	w.Write([]byte("✅ Оценка вакансии завершена: оценка: 8/10; рекомендация: apply; причина: Подходит\n"))
	w.Write([]byte("✅ Отклик подтверждён hh.ru\n"))
	w.Write([]byte("❌ Agent failed: не удалось добавить письмо"))
	w.Flush()
	if len(s.state.Cards) != 1 {
		t.Fatalf("cards: %+v", s.state.Cards)
	}
	c := s.state.Cards[0]
	if c.Title != "ML инженер" || c.Score != 8 || c.Stage != "Готово" {
		t.Fatalf("card: %+v", c)
	}
	if !strings.Contains(s.state.Output, "Agent failed: не удалось добавить письмо") {
		t.Fatal("lost error diagnostics")
	}
}

func TestReviewIsNotLostAtTaskCompletion(t *testing.T) {
	s := &server{}
	s.appendOutput("Анализирую вакансию 1/0: Аналитик | URL: https://hh.ru/vacancy/123\n")
	s.appendOutput("Оценка вакансии завершена: оценка: 6/10; рекомендация: review; причина: Уточнить опыт\n")
	s.appendOutput("📨 ОТКЛИКИ: 0 из 2\nНедобор: 2. Достигнут лимит подходящих вакансий.\n")
	s.setFinished("Поиск завершён", "")
	c := s.state.Cards[0]
	if c.Score != 6 || c.Status == "error" || c.Result != "Нужна ручная проверка" {
		t.Fatal(c)
	}
	if !strings.Contains(s.state.Summary, "0 из 2") || !strings.Contains(s.state.Summary, "Недобор: 2") {
		t.Fatal(s.state.Summary)
	}
}
