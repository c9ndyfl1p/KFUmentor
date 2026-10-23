package contentcheck

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInspectionViews(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"р3зюм3", "резюме"}, {"рeзюмe", "резюме"}, {"р е з ю м е", "резюме"},
		{"ＡＢＣ", "ABC"}, {"&#1088;&#1077;&#1079;&#1102;&#1084;&#1077;", "резюме"},
		{"SGVsbG8gd29ybGQgZnJvbSByZXN1bWU=", "Hello world from resume"},
	} {
		views, err := inspectionViews(tc.input)
		if err != nil || !strings.Contains(strings.Join(views, "\n"), tc.want) {
			t.Fatalf("%q: %v %v", tc.input, views, err)
		}
	}
}
func TestDecodedHiddenCharactersBlocked(t *testing.T) {
	for _, s := range []string{`{"summary":"test\u200btext"}`, "abc\ufe0fdef", "%E2%80%8B", `{"skills":["a\u0001b"]}`} {
		if _, err := inspectionViews(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestNormalTechnicalTermsPreserved(t *testing.T) {
	for _, s := range []string{"C++ C# .NET 1С 3D SQL Python 3.12", "+7 999 123 45 67", "2024–2026 https://example.org/portfolio"} {
		views, err := inspectionViews(s)
		if err != nil {
			t.Fatal(s, err)
		}
		if len(views) != 0 {
			t.Fatal("normal text changed", s, views)
		}
	}
}
func TestWaitingCheckCanCancel(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	c := &Checker{Model: modelFunc(func(context.Context, string, string, json.RawMessage) (string, error) {
		close(started)
		<-release
		return `{"decision":"allow","kind":"resume","reasons":[]}`, nil
	})}
	done := make(chan error, 1)
	go func() { done <- c.Check(context.Background(), Resume, "Навыки SQL") }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.Check(ctx, Resume, "Другие навыки") }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled check passed")
		}
	case <-time.After(time.Second):
		t.Fatal("queue ignored cancellation")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
