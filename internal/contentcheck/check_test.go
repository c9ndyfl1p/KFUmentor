package contentcheck

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type modelFunc func(context.Context, string, string, json.RawMessage) (string, error)

func (f modelFunc) GenerateStructured(c context.Context, p, s string, j json.RawMessage) (string, error) {
	return f(c, p, s, j)
}
func TestFailClosedVerdicts(t *testing.T) {
	for _, tc := range []struct {
		response string
		allow    bool
	}{
		{`{"decision":"allow","kind":"resume","reasons":[]}`, true},
		{`{"decision":"reject","kind":"resume","reasons":["Посторонняя агитация в разделе о себе"]}`, false},
		{`{"decision":"uncertain","kind":"resume","reasons":["Не удалось проверить"]}`, false},
		{`{"decision":"allow","kind":"other","reasons":[]}`, false},
		{`{"decision":"allow","kind":"cover_letter","reasons":[]}`, false},
		{`{"decision":"allow","kind":"resume","reasons":["Противоречие"]}`, false},
		{`{"decision":"allow","kind":"resume"}`, false},
		{`{"decision":"allow","kind":"resume","reasons":null}`, false},
		{`{"decision":"allow","kind":"resume","reasons":[]} {}`, false},
		{`{"decision":"allow","kind":"resume","reasons":[],"bypass":true}`, false},
		{`not JSON`, false},
	} {
		c := &Checker{Model: modelFunc(func(context.Context, string, string, json.RawMessage) (string, error) { return tc.response, nil })}
		err := c.Check(context.Background(), Resume, "Начинающий аналитик. Навыки: Excel. Учебный проект.")
		if (err == nil) != tc.allow {
			t.Errorf("%s: %v", tc.response, err)
		}
	}
}
func TestUnavailableModelNeverApproves(t *testing.T) {
	for _, c := range []*Checker{nil, {}, {Model: modelFunc(func(context.Context, string, string, json.RawMessage) (string, error) {
		return "", errors.New("HTTP 429")
	})}} {
		if c.Check(context.Background(), Resume, "Резюме. SQL") == nil {
			t.Fatal("unavailable checker approved content")
		}
	}
}
func TestExactTextKindCacheAndFullInput(t *testing.T) {
	calls := 0
	c := &Checker{Model: modelFunc(func(ctx context.Context, prompt, system string, schema json.RawMessage) (string, error) {
		calls++
		if system != SystemPrompt || !json.Valid(schema) {
			t.Fatal("wrong policy/schema")
		}
		var request struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		json.Unmarshal([]byte(prompt), &request)
		if strings.Contains(request.Text, "Посторонний призыв") {
			return `{"decision":"reject","kind":"other","reasons":["Посторонний призыв"]}`, nil
		}
		return `{"decision":"allow","kind":"` + request.Kind + `","reasons":[]}`, nil
	})}
	text := "Резюме. Учебный проект, навыки SQL."
	for i := 0; i < 2; i++ {
		if err := c.Check(context.Background(), Resume, text); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("identical text was not cached")
	}
	if err := c.Check(context.Background(), Letter, text); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("wrong document kind reused approval")
	}
	for i := 0; i < 2; i++ {
		if c.Check(context.Background(), Resume, text+" Посторонний призыв") == nil {
			t.Fatal("edit reused approval")
		}
	}
	if calls != 4 {
		t.Fatal("rejections must not be cached")
	}
}
func TestUnreadableInputAndCancellation(t *testing.T) {
	c := &Checker{Model: modelFunc(func(context.Context, string, string, json.RawMessage) (string, error) {
		t.Fatal("should not call model")
		return "", nil
	})}
	for _, s := range []string{"", string([]byte{255}), "Резюме\u202eтекст", strings.Repeat("я", 24001), "Резюме\x00"} {
		if c.Check(context.Background(), Resume, s) == nil {
			t.Fatal("accepted", s)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Check(ctx, Resume, "SQL") == nil {
		t.Fatal("cancelled check passed")
	}
}
