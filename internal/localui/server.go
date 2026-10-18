package localui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

//go:embed static/*
var staticFiles embed.FS

const (
	localResumePath = "data/resume.local.json"
	maxOutputBytes  = 128 * 1024
)

type taskInput struct {
	Task      string `json:"task"`
	AutoApply bool   `json:"autoApply"`
	Visible   bool   `json:"visible"`
}

type profileInput struct {
	Name       string   `json:"name"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Skills     []string `json:"skills"`
	Experience string   `json:"experience"`
}

type taskState struct {
	Running   bool       `json:"running"`
	Status    string     `json:"status"`
	Output    string     `json:"output"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
}

type server struct {
	cfg   types.Config
	mu    sync.Mutex
	state taskState
}

func Start(cfg types.Config, address string) error {
	if address == "" {
		address = "127.0.0.1:8787"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid web address %q: %w", address, err)
	}
	if host != "localhost" && net.ParseIP(host) == nil {
		return fmt.Errorf("web interface must bind to a local address, got %q", host)
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return fmt.Errorf("web interface must bind to localhost, got %q", host)
	}

	app := &server{cfg: cfg, state: taskState{Status: "Готов к поиску"}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", app.handleIndex)
	static, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /api/profile", app.handleGetProfile)
	mux.HandleFunc("PUT /api/profile", app.handleSaveProfile)
	mux.HandleFunc("POST /api/resume", app.handleUploadResume)
	mux.HandleFunc("GET /api/state", app.handleState)
	mux.HandleFunc("POST /api/run", app.handleRun)
	mux.HandleFunc("POST /api/clear", app.handleClear)

	httpServer := &http.Server{
		Addr:              address,
		Handler:           app.localOnly(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("AI Job Agent is ready at http://%s", address)
	return httpServer.ListenAndServe()
}

func (s *server) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "localhost" {
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				http.Error(w, "local access only", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "interface files unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.readProfile()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profileInput{
		Name:       profile.Name,
		Title:      profile.Title,
		Summary:    profile.Summary,
		Skills:     profile.Skills,
		Experience: formatExperience(profile.Experience),
	})
}

func (s *server) handleSaveProfile(w http.ResponseWriter, r *http.Request) {
	var input profileInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(input.Name) > 200 || len(input.Title) > 200 || len(input.Summary) > 5000 || len(input.Experience) > 12000 || len(input.Skills) > 100 {
		writeError(w, http.StatusBadRequest, "Проверьте длину полей профиля")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Skills = cleanSkills(input.Skills)

	resume := types.Resume{
		Name:       input.Name,
		Title:      input.Title,
		Summary:    input.Summary,
		Skills:     input.Skills,
		Experience: parseExperience(input.Experience),
	}
	if current, err := s.readProfile(); err == nil {
		resume.Education = current.Education
		resume.Languages = current.Languages
		resume.ContactInfo = current.ContactInfo
	}

	data, err := json.MarshalIndent(resume, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось собрать профиль")
		return
	}
	if err := os.MkdirAll(filepath.Dir(localResumePath), 0700); err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось создать папку профиля")
		return
	}
	if err := os.WriteFile(localResumePath, data, 0600); err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось сохранить профиль")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "Профиль сохранён на этом компьютере"})
}

func (s *server) handleUploadResume(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Не удалось прочитать файл. Максимальный размер — 2 МБ")
		return
	}
	file, header, err := r.FormFile("resume")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Выберите файл резюме")
		return
	}
	defer file.Close()
	if !strings.EqualFold(filepath.Ext(header.Filename), ".json") {
		writeError(w, http.StatusBadRequest, "Пока поддерживается JSON-файл профиля")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		writeError(w, http.StatusBadRequest, "Файл не удалось прочитать или он больше 1 МБ")
		return
	}
	var profile types.Resume
	if err := json.Unmarshal(data, &profile); err != nil {
		writeError(w, http.StatusBadRequest, "Файл должен быть корректным JSON-профилем резюме")
		return
	}
	if strings.TrimSpace(profile.Name) == "" && strings.TrimSpace(profile.Title) == "" && len(profile.Skills) == 0 {
		writeError(w, http.StatusBadRequest, "В резюме не найдены имя, должность или навыки")
		return
	}
	if err := os.MkdirAll(filepath.Dir(localResumePath), 0700); err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось создать папку профиля")
		return
	}
	if err := os.WriteFile(localResumePath, data, 0600); err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось сохранить профиль")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "Резюме прикреплено и сохранено локально"})
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, state)
}

func (s *server) handleClear(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.state.Output = ""
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "Журнал очищен"})
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	var input taskInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Task = strings.TrimSpace(input.Task)
	if input.Task == "" || len(input.Task) > 2000 {
		writeError(w, http.StatusBadRequest, "Введите задачу до 2000 символов")
		return
	}
	profile, err := s.readProfile()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if input.AutoApply && (strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.Title) == "" || len(profile.Skills) == 0) {
		writeError(w, http.StatusBadRequest, "Сначала заполните имя, должность и навыки в профиле")
		return
	}

	s.mu.Lock()
	if s.state.Running {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "Задача уже выполняется")
		return
	}
	startedAt := time.Now()
	s.state = taskState{Running: true, Status: "Запускаю браузер…", StartedAt: &startedAt}
	s.mu.Unlock()

	args := []string{
		"--task=" + input.Task,
		"--headless=" + fmt.Sprint(!input.Visible),
		"--auto-apply=" + fmt.Sprint(input.AutoApply),
		"--resume=" + localResumePath,
		"--profile=" + s.cfg.ProfileDir,
		"--model-url=" + s.cfg.ModelURL,
		"--model=" + s.cfg.ModelName,
		"--debug=" + fmt.Sprint(s.cfg.Debug),
	}
	executable, err := os.Executable()
	if err != nil {
		s.setFinished("Не удалось запустить агент", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cmd := exec.Command(executable, args...)
	output := &outputWriter{server: s}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		s.setFinished("Не удалось запустить агент", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "Задача запущена"})
	go func() {
		err := cmd.Wait()
		if err != nil {
			s.appendOutput("\nОшибка: " + err.Error() + "\n")
			s.setFinished("Задача завершилась с ошибкой", err.Error())
			return
		}
		s.setFinished("Поиск завершён", "")
	}()
}

func (s *server) readProfile() (types.Resume, error) {
	data, err := os.ReadFile(localResumePath)
	if errors.Is(err, os.ErrNotExist) {
		return types.Resume{}, nil
	}
	if err != nil {
		return types.Resume{}, fmt.Errorf("Не удалось прочитать локальный профиль: %w", err)
	}
	var profile types.Resume
	if err := json.Unmarshal(data, &profile); err != nil {
		return types.Resume{}, fmt.Errorf("Локальный профиль повреждён: %w", err)
	}
	return profile, nil
}

func parseExperience(text string) []types.Experience {
	lines := strings.Split(text, "\n")
	items := make([]types.Experience, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			items = append(items, types.Experience{Position: line})
		}
	}
	return items
}

func formatExperience(items []types.Experience) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		parts := []string{item.Position, item.Company, item.Period}
		line := strings.TrimSpace(strings.Join(parts, " · "))
		if len(item.Responsibilities) > 0 {
			line += " — " + strings.Join(item.Responsibilities, ", ")
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func cleanSkills(skills []string) []string {
	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(skills))
	for _, skill := range skills {
		for _, part := range strings.Split(skill, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !seen[strings.ToLower(part)] {
				seen[strings.ToLower(part)] = true
				cleaned = append(cleaned, part)
			}
		}
	}
	return cleaned
}

func (s *server) appendOutput(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Output += text
	if len(s.state.Output) > maxOutputBytes {
		s.state.Output = s.state.Output[len(s.state.Output)-maxOutputBytes:]
	}
	if s.state.Running {
		s.state.Status = "Агент выполняет задачу"
	}
}

func (s *server) setFinished(status, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Running = false
	s.state.Status = status
	if detail != "" {
		s.state.Status += ": " + detail
	}
}

type outputWriter struct{ server *server }

func (w *outputWriter) Write(data []byte) (int, error) {
	w.server.appendOutput(string(data))
	return len(data), nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("ожидался один JSON-объект")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

var _ io.Writer = (*outputWriter)(nil)
