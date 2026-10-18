package localui

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
)

func testProfile(t *testing.T) types.Resume {
	t.Helper()
	t.Chdir(t.TempDir())
	profile := types.Resume{Name: "Исходный кандидат", Title: "Разработчик", Skills: []string{"Go"}, Experience: []types.Experience{{Position: "Разработчик", Company: "Компания", Period: "2024–2025", Responsibilities: []string{"Разрабатывал API"}}}, Projects: []types.Project{{Name: "Учебный проект", Description: "Классификация текстов", Technologies: []string{"Python"}, Link: "https://example.test/project"}}, Achievements: []types.Achievement{{Name: "Конкурс", Result: "участник", Date: "2025", Organizer: "Организатор", Link: "https://example.test/contest"}}, Education: []types.Education{{Degree: "Бакалавр", University: "Университет", Year: "2025"}}}
	if err := saveProfile(profile); err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestImportPreviewDoesNotWriteUntilConfirmed(t *testing.T) {
	old := testProfile(t)
	before, _ := os.ReadFile(localResumePath)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	file, _ := w.CreateFormFile("resume", "resume.json")
	file.Write([]byte(`{"name":"Новый кандидат","skills":["Python"],"projects":[{"name":"Учебный проект"}]}`))
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/resume", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	recorder := httptest.NewRecorder()
	s := &server{}
	s.handleUploadResume(recorder, req)
	if recorder.Code != 200 {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	after, _ := os.ReadFile(localResumePath)
	if !bytes.Equal(before, after) {
		t.Fatal("preview changed existing profile")
	}
	var preview struct {
		Profile types.Resume `json:"profile"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Profile.Name == old.Name {
		t.Fatal("wrong preview")
	}
	data, _ := json.Marshal(preview.Profile)
	recorder = httptest.NewRecorder()
	s.handleConfirmResume(recorder, httptest.NewRequest(http.MethodPost, "/api/resume/confirm", bytes.NewReader(data)))
	if recorder.Code != 200 {
		t.Fatal(recorder.Body.String())
	}
	saved, err := s.readProfile()
	if err != nil || saved.Name != "Новый кандидат" || len(saved.Projects) != 1 {
		t.Fatal(saved, err)
	}
}

func TestInvalidConfirmationKeepsExistingProfile(t *testing.T) {
	testProfile(t)
	before, _ := os.ReadFile(localResumePath)
	s := &server{}
	recorder := httptest.NewRecorder()
	s.handleConfirmResume(recorder, httptest.NewRequest(http.MethodPost, "/api/resume/confirm", strings.NewReader(`{}`)))
	after, _ := os.ReadFile(localResumePath)
	if recorder.Code != 400 || !bytes.Equal(before, after) {
		t.Fatal("invalid confirmation modified profile")
	}
}

func TestBasicProfileSaveKeepsStructuredImportedDetails(t *testing.T) {
	original := testProfile(t)
	data, _ := json.Marshal(profileInput{Name: original.Name, Title: original.Title, Skills: original.Skills, Experience: formatExperience(original.Experience), Projects: formatProjects(original.Projects), Achievements: formatAchievements(original.Achievements)})
	s := &server{}
	recorder := httptest.NewRecorder()
	s.handleSaveProfile(recorder, httptest.NewRequest(http.MethodPut, "/api/profile", bytes.NewReader(data)))
	if recorder.Code != 200 {
		t.Fatal(recorder.Body.String())
	}
	saved, err := s.readProfile()
	if err != nil || saved.Experience[0].Company != "Компания" || saved.Projects[0].Link != original.Projects[0].Link || len(saved.Education) != 1 || len(saved.Achievements) != 1 || saved.Achievements[0].Link != original.Achievements[0].Link {
		t.Fatal(saved, err)
	}
	info, _ := os.Stat(localResumePath)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	files, _ := filepath.Glob("data/.resume-*.tmp")
	if len(files) > 0 {
		t.Fatal("temporary profile left behind")
	}
}

func TestResumeBrowserPreview(t *testing.T) {
	if os.Getenv("KFUMENTOR_IMPORT_BROWSER_SMOKE") != "1" {
		t.Skip("opt-in Chromium UI test")
	}
	testProfile(t)
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /static/{file}", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/" + r.PathValue("file"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".js") {
			w.Header().Set("Content-Type", "application/javascript")
		}
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css")
		}
		w.Write(data)
	})
	mux.HandleFunc("GET /api/profile", s.handleGetProfile)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/resume", s.handleUploadResume)
	mux.HandleFunc("POST /api/resume/confirm", s.handleConfirmResume)
	mux.HandleFunc("GET /api/profile/details", s.handleProfileDetails)
	web := httptest.NewServer(mux)
	defer web.Close()
	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	var errors []string
	page.OnPageError(func(err error) { errors = append(errors, err.Error()) })
	if _, err := page.Goto(web.URL); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#open-resume").Click(); err != nil {
		t.Fatal(err)
	}
	file := []playwright.InputFile{{Name: "resume.json", MimeType: "application/json", Buffer: []byte(`{"name":"Импортированный кандидат","skills":["Python"],"projects":[{"name":"Учебный проект","description":"NLP","technologies":["Python"],"link":""}],"achievements":[{"name":"Тестовый конкурс","result":"участник","date":"2025","organizer":"Тестовый университет","link":""}]}`)}}
	if err := page.Locator("#resume-file").SetInputFiles(file); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#upload-resume").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#confirm-resume").WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible}); err != nil {
		t.Fatal(err)
	}
	if name, err := page.Locator("[data-section='basic'] [data-field='name']").InputValue(); err != nil || name != "Импортированный кандидат" {
		t.Fatal(name, err)
	}
	if path := os.Getenv("KFUMENTOR_IMPORT_SCREENSHOT"); path != "" {
		if err := page.SetViewportSize(1440, 1000); err != nil {
			t.Fatal(err)
		}
		if _, err := page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String(path)}); err != nil {
			t.Fatal(err)
		}
		if err := page.SetViewportSize(390, 844); err != nil {
			t.Fatal(err)
		}
		if _, err := page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String(strings.TrimSuffix(path, ".png") + "-mobile.png")}); err != nil {
			t.Fatal(err)
		}
		if err := page.SetViewportSize(1440, 1000); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := s.readProfile()
	if current.Name != "Исходный кандидат" {
		t.Fatal("preview overwrote current profile")
	}
	if err := page.Locator("[data-section='basic'] [data-field='name']").Fill("Проверенный кандидат"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#confirm-resume").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#resume-dialog").WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateHidden}); err != nil {
		t.Fatal(err)
	}
	current, _ = s.readProfile()
	if current.Name != "Проверенный кандидат" || len(current.Projects) != 1 || len(current.Achievements) != 1 || current.Achievements[0].Result != "участник" {
		t.Fatal(current)
	}
	if len(errors) > 0 {
		t.Fatal(errors)
	}
}
