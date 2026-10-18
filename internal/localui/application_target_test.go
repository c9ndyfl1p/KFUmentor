package localui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationTargetValidationAndProcessArgument(t *testing.T) {
	s := &server{}
	input := taskInput{Task: "ML", ApplicationTarget: 12, AutoApply: true}
	if err := normalizeApplicationTarget(&input); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(s.taskArguments(input), " ")
	if !strings.Contains(args, "--application-target=12") || !strings.Contains(args, "--auto-apply=true") {
		t.Fatal(args)
	}
	input.AutoApply = false
	if args := strings.Join(s.taskArguments(input), " "); !strings.Contains(args, "--auto-apply=false") {
		t.Fatal(args)
	}
	legacy := taskInput{}
	if err := normalizeApplicationTarget(&legacy); err != nil || legacy.ApplicationTarget != 5 {
		t.Fatal(legacy, err)
	}
	for _, body := range []string{`{"task":"ML","applicationTarget":-1}`, `{"task":"ML","applicationTarget":101}`, `{"task":"ML","applicationTarget":1.5}`} {
		recorder := httptest.NewRecorder()
		s.handleRun(recorder, httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(body)))
		if recorder.Code != 400 {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
	}
}
