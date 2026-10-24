package resumeimport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/contentcheck"
)

type contentModel func(context.Context, string, string, json.RawMessage) (string, error)

func (f contentModel) GenerateStructured(c context.Context, p, s string, j json.RawMessage) (string, error) {
	return f(c, p, s, j)
}
func TestRejectBeforeExtraction(t *testing.T) {
	calls := 0
	model := contentModel(func(_ context.Context, p, s string, _ json.RawMessage) (string, error) {
		calls++
		if s != contentcheck.SystemPrompt || !strings.Contains(p, "Посторонний призыв") {
			t.Fatal("raw document did not reach moderation")
		}
		return `{"decision":"reject","kind":"other","reasons":["Посторонний призыв"]}`, nil
	})
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"resume.json", []byte(`{"name":"Кандидат","summary":"Посторонний призыв","skills":["SQL"]}`)},
		{"resume.docx", docxFixture(t, `<w:p><w:r><w:t>Навыки SQL. Посторонний призыв</w:t></w:r></w:p>`)},
	} {
		if _, err := Parse(context.Background(), tc.name, tc.data, model); err == nil {
			t.Fatal("accepted mixed document")
		}
	}
	if calls != 2 {
		t.Fatal("untrusted content reached extraction", calls)
	}
	if _, err := Parse(context.Background(), "r.json", []byte(`{"skills":["SQL"]}`), nil); err == nil {
		t.Fatal("JSON bypassed unavailable moderation")
	}
	if _, err := Parse(context.Background(), "r.json", []byte(`{"summary":"x","summary":"y"}`), model); err == nil {
		t.Fatal("duplicate field accepted")
	}
}
func TestDOCXUnsupportedContent(t *testing.T) {
	for _, name := range []string{"word/media/photo.png", "word/embeddings/file.bin", "word/comments.xml", "word/footnotes.xml", "word/vbaProject.bin"} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, _ := z.Create("word/document.xml")
		fmt.Fprint(f, `<document><p><t>Кандидат. Навыки: SQL, Excel.</t></p></document>`)
		f, _ = z.Create(name)
		fmt.Fprint(f, "unreadable content")
		z.Close()
		if _, err := ExtractText("r.docx", b.Bytes()); err == nil || !strings.Contains(err.Error(), "без фото") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, tag := range []string{"del", "ins", "object", "drawing", "pict", "altChunk", "instrText"} {
		body := `<w:p><w:r><w:t>Навыки: SQL, Excel. Учебный проект.</w:t><w:` + tag + `/></w:r></w:p>`
		if _, err := ExtractText("r.docx", docxFixture(t, body)); err == nil {
			t.Fatal("unsupported element accepted", tag)
		}
	}
}
func TestPDFImagesAndFormsRejected(t *testing.T) {
	for _, extra := range []string{`/Resources << /XObject << /Image 4 0 R >> >>`, `/Annots [<< /Subtype /Widget >>]`} {
		objects := []string{`<< /Type /Catalog /Pages 2 0 R >>`, `<< /Type /Pages /Count 1 /Kids [3 0 R] >>`, `<< /Type /Page /Parent 2 0 R ` + extra + ` >>`, `<< /Type /XObject /Subtype /Image >>`}
		var b bytes.Buffer
		b.WriteString("%PDF-1.4\n")
		offsets := []int{0}
		for i, o := range objects {
			offsets = append(offsets, b.Len())
			fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
		}
		xref := b.Len()
		fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
		for _, offset := range offsets[1:] {
			fmt.Fprintf(&b, "%010d 00000 n \n", offset)
		}
		fmt.Fprintf(&b, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
		if _, err := ExtractText("r.pdf", b.Bytes()); err == nil || !strings.Contains(err.Error(), "PDF содержит") {
			t.Fatal(err)
		}
	}
}
