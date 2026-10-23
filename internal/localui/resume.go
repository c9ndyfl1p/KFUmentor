package localui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/contentcheck"
	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/resumeimport"
	"github.com/mihnpro/Ai_agent_2/internal/types"
)

func (s *server) handleUploadResume(w http.ResponseWriter, r *http.Request) {
	if !s.importMu.TryLock() {
		writeError(w, http.StatusConflict, "Уже разбираю резюме. Дождитесь результата или отмените загрузку.")
		return
	}
	defer s.importMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, resumeimport.MaxFileBytes+(1<<20))
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Не удалось прочитать файл. Максимальный размер — 10 МБ")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("resume")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Выберите файл резюме")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, resumeimport.MaxFileBytes+1))
	if err != nil || len(data) > resumeimport.MaxFileBytes {
		writeError(w, http.StatusBadRequest, "Файл не удалось прочитать или он больше 10 МБ")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	client := s.contentChecker().Model
	preview, err := resumeimport.Parse(ctx, header.Filename, data, client)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Preview only. Neither the original document nor the candidate's existing
	// profile is written until the user explicitly confirms the edited fields.
	writeJSON(w, http.StatusOK, preview)
}

func (s *server) handleConfirmResume(w http.ResponseWriter, r *http.Request) {
	var profile types.Resume
	if err := decodeJSON(w, r, &profile); err != nil {
		writeError(w, http.StatusBadRequest, "Не удалось прочитать профиль: "+err.Error())
		return
	}
	if err := s.saveCheckedProfile(r.Context(), profile); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "Проверенный профиль сохранён на этом компьютере"})
}

func (s *server) handleProfileDetails(w http.ResponseWriter, r *http.Request) {
	profile, err := s.readProfile()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func saveProfile(profile types.Resume) error {
	if err := resumeimport.ValidateProfile(profile); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось собрать профиль")
	}
	if err := os.MkdirAll(filepath.Dir(localResumePath), 0700); err != nil {
		return fmt.Errorf("не удалось создать папку профиля")
	}
	tmp, err := os.CreateTemp(filepath.Dir(localResumePath), ".resume-*.tmp")
	if err != nil {
		return fmt.Errorf("не удалось сохранить профиль")
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("не удалось записать профиль")
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("не удалось завершить сохранение профиля")
	}
	if err := os.Rename(tmp.Name(), localResumePath); err != nil {
		return fmt.Errorf("не удалось заменить профиль")
	}
	return nil
}

func formatProjects(projects []types.Project) string {
	var lines []string
	for _, project := range projects {
		line := strings.TrimSpace(project.Name)
		if project.Description != "" {
			if line != "" {
				line += " — "
			}
			line += project.Description
		}
		if len(project.Technologies) > 0 {
			line += " · " + strings.Join(project.Technologies, ", ")
		}
		if project.Link != "" {
			line += " · " + project.Link
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.Join(lines, "\n")
}

func parseProjects(text string) []types.Project {
	var projects []types.Project
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			projects = append(projects, types.Project{Description: line})
		}
	}
	return projects
}

func formatAchievements(achievements []types.Achievement) string {
	var lines []string
	for _, item := range achievements {
		var parts []string
		for _, part := range []string{item.Name, item.Result, item.Date, item.Organizer, item.Link} {
			if strings.TrimSpace(part) != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, " · "))
		}
	}
	return strings.Join(lines, "\n")
}

func parseAchievements(text string) []types.Achievement {
	var items []types.Achievement
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			items = append(items, types.Achievement{Name: line})
		}
	}
	return items
}

func (s *server) contentChecker() *contentcheck.Checker {
	s.moderationOnce.Do(func() {
		if s.moderation == nil {
			s.moderation = &contentcheck.Checker{Model: llm.NewOllamaClient(s.cfg.ModelURL, s.cfg.ModelName)}
		}
	})
	return s.moderation
}
func (s *server) saveCheckedProfile(ctx context.Context, profile types.Resume) error {
	if err := resumeimport.ValidateProfile(profile); err != nil {
		return err
	}
	if err := s.contentChecker().CheckProfile(ctx, profile); err != nil {
		return err
	}
	return saveProfile(profile)
}
