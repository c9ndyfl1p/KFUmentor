package resumeimport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/contentcheck"
	"github.com/mihnpro/Ai_agent_2/internal/llm"
)

func docxFixture(t *testing.T, body string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	part, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(part, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>%s</w:body></w:document>`, body)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// Minimal real PDF fixture with a font, content stream and correct xref offsets.
func pdfFixture(lines []string) []byte {
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 50 740 Td\n")
	for _, line := range lines {
		content.WriteString("(" + strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(line) + ") Tj 0 -18 Td\n")
	}
	content.WriteString("ET\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()),
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, output.Len())
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return output.Bytes()
}

func TestExtractPDFAndDOCX(t *testing.T) {
	text, err := ExtractText("resume.PDF", pdfFixture([]string{"Test Candidate", "Skills: Python, SQL", "Study project: text classification"}))
	if err != nil || !strings.Contains(text, "Python") || !strings.Contains(text, "Test Candidate\nSkills:") {
		t.Fatal(text, err)
	}
	docx := docxFixture(t, `<w:p><w:r><w:t>Тестовый кандидат</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Навыки: Python, SQL</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:p><w:r><w:t>Учебный проект</w:t></w:r></w:p>`)
	text, err = ExtractText("resume.docx", docx)
	if err != nil || !strings.Contains(text, "Python") || !strings.Contains(text, "Тестовый кандидат") || strings.Contains(text, "ложная") {
		t.Fatal(text, err)
	}
}

func TestCyrillicPDFTextLayer(t *testing.T) {
	line := "Тестовый кандидат. Навыки: Python и SQL."
	var encoded, cmap strings.Builder
	runes := []rune(line)
	for i, r := range runes {
		fmt.Fprintf(&encoded, "%04X", i+1)
		fmt.Fprintf(&cmap, "<%04X> <%04X>\n", i+1, r)
	}
	mapping := fmt.Sprintf("/CIDInit /ProcSet findresource begin 12 dict begin begincmap /CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def /CMapName /Test-UCS def /CMapType 2 def 1 begincodespacerange <0000> <FFFF> endcodespacerange %d beginbfchar\n%sendbfchar endcmap CMapName currentdict /CMap defineresource pop end end", len(runes), cmap.String())
	content := "BT /F1 12 Tf 50 740 Td <" + encoded.String() + "> Tj ET\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type0 /BaseFont /TestFont /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 7 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /TestFont /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 600 >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(mapping), mapping),
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, output.Len())
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	text, err := ExtractText("russian.pdf", output.Bytes())
	if err != nil || strings.TrimSpace(text) != line {
		t.Fatal(text, err)
	}
}

func TestInvalidDocumentsAndScans(t *testing.T) {
	for _, test := range []struct {
		name    string
		data    []byte
		message string
	}{
		{"scan.pdf", pdfFixture(nil), "OCR"},
		{"fake.pdf", []byte("not a PDF"), "PDF"},
		{"resume.doc", []byte("old format"), "DOCX"},
		{"fake.docx", []byte("not a ZIP"), "DOCX"},
		{"huge.pdf", make([]byte, MaxFileBytes+1), "10 МБ"},
	} {
		_, err := ExtractText(test.name, test.data)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%s: %v", test.name, err)
		}
	}
}

type fakeModel struct {
	called   bool
	response string
}

func (m *fakeModel) GenerateStructured(ctx context.Context, prompt, system string, schema json.RawMessage) (string, error) {
	m.called = true
	if system == contentcheck.SystemPrompt {
		return `{"decision":"allow","kind":"resume","reasons":[]}`, nil
	}
	return m.response, nil
}

func TestJSONRequiresModerationAndKeepsProjects(t *testing.T) {
	model := &fakeModel{}
	preview, err := Parse(context.Background(), "resume.json", []byte(`{"name":"Тест","projects":[{"name":"Учебный проект","description":"Классификация текстов","technologies":["Python"],"link":""}]}`), model)
	if err != nil || !model.called || len(preview.Profile.Projects) != 1 {
		t.Fatal(preview, err)
	}
	if _, err := Parse(context.Background(), "resume.json", []byte(`{"name":"Тест"} {}`), model); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}

func TestModelQuotesMustExistInSource(t *testing.T) {
	docx := docxFixture(t, `<w:p><w:r><w:t>Тестовый кандидат. Навыки: Python, SQL. Учебный проект.</w:t></w:r></w:p>`)
	model := &fakeModel{response: `{"profile":{"name":"Тестовый кандидат","skills":["Python","SQL"]},"evidence":[{"section":"skills","quote":"Python, SQL"},{"section":"experience","quote":"10 лет коммерческого опыта"}]}`}
	preview, err := Parse(context.Background(), "resume.docx", docx, model)
	if err != nil || !model.called || len(preview.Evidence) != 1 || len(preview.Warnings) < 2 {
		t.Fatal(preview, err)
	}
}

func TestLocalImportModelSmoke(t *testing.T) {
	if os.Getenv("KFUMENTOR_IMPORT_MODEL_SMOKE") != "1" {
		t.Skip("opt-in local model import")
	}
	data := pdfFixture([]string{"Test Candidate", "Target role: Junior ML Engineer", "Skills: Python, SQL", "Projects: Study project for text classification.", "Education: Computer Science, Example University, 2025.", "Email: candidate@example.test"})
	client := llm.NewOllamaClient("http://127.0.0.1:11434", "qwen3.5:4b-q4_K_M")
	preview, err := Parse(context.Background(), "resume.pdf", data, client)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Profile.Name != "Test Candidate" || len(preview.Profile.Skills) == 0 || len(preview.Profile.Projects) == 0 {
		t.Fatalf("missing facts: %+v", preview.Profile)
	}
	if len(preview.Profile.Experience) > 0 {
		t.Fatal("study project was converted into employment")
	}
	t.Logf("PDF → model → preview: name=%s, skills=%v, projects=%d, evidence=%d", preview.Profile.Name, preview.Profile.Skills, len(preview.Profile.Projects), len(preview.Evidence))
	docx := docxFixture(t, `<w:p><w:r><w:t>Мария Тестовая</w:t></w:r></w:p><w:p><w:r><w:t>Желаемая должность: начинающий ML-инженер</w:t></w:r></w:p><w:p><w:r><w:t>Навыки: Python, SQL</w:t></w:r></w:p><w:p><w:r><w:t>Учебный проект: классификация текстов на Python. Коммерческого опыта нет.</w:t></w:r></w:p>`)
	preview, err = Parse(context.Background(), "resume.docx", docx, client)
	if err != nil || preview.Profile.Name != "Мария Тестовая" || len(preview.Profile.Projects) == 0 || len(preview.Profile.Experience) > 0 {
		t.Fatal(preview.Profile, err)
	}
	t.Logf("DOCX → model → preview: name=%s, skills=%v, projects=%d", preview.Profile.Name, preview.Profile.Skills, len(preview.Profile.Projects))
}

func TestJSONAchievementsRoundTrip(t *testing.T) {
	data := []byte(`{"name":"Тест","achievements":[{"name":"Конкурс","result":"участник","date":"2025","organizer":"КФУ","link":""}]}`)
	preview, err := Parse(context.Background(), "resume.json", data, &fakeModel{})
	if err != nil || len(preview.Profile.Achievements) != 1 || preview.Profile.Achievements[0].Result != "участник" || len(preview.Profile.Education) != 0 {
		t.Fatal(preview, err)
	}
}

func TestMissingAchievementsKeepOriginalSection(t *testing.T) {
	docx := docxFixture(t, `<w:p><w:r><w:t>Тестовый кандидат</w:t></w:r></w:p><w:p><w:r><w:t>Личные достижения: участник конкурса «Старт», 2025. Призового места нет.</w:t></w:r></w:p><w:p><w:r><w:t>Образование: КФУ, бакалавриат, 2026.</w:t></w:r></w:p>`)
	model := &fakeModel{response: `{"profile":{"name":"Тестовый кандидат","education":[{"degree":"бакалавриат","university":"КФУ","year":"2026"}],"achievements":[]},"evidence":[]}`}
	preview, err := Parse(context.Background(), "resume.docx", docx, model)
	if err != nil || len(preview.Profile.Achievements) != 1 || len(preview.Profile.Education) != 1 {
		t.Fatal(preview, err)
	}
	result := preview.Profile.Achievements[0].Result
	if !strings.Contains(result, "Призового места нет") || strings.Contains(result, "Образование") {
		t.Fatal(result)
	}
}

func TestLocalAchievementModelSmoke(t *testing.T) {
	if os.Getenv("KFUMENTOR_ACHIEVEMENT_MODEL_SMOKE") != "1" {
		t.Skip("opt-in local model achievement classification")
	}
	docx := docxFixture(t, `<w:p><w:r><w:t>Мария Тестовая</w:t></w:r></w:p><w:p><w:r><w:t>Навыки: Python, SQL</w:t></w:r></w:p><w:p><w:r><w:t>Образование: бакалавриат КФУ, 2026.</w:t></w:r></w:p><w:p><w:r><w:t>Личные достижения: участие в конкурсе «Цифровой старт», 2025. Организатор: Университет Иннополис. Призового места нет.</w:t></w:r></w:p>`)
	client := llm.NewOllamaClient("http://127.0.0.1:11434", "qwen3.5:4b-q4_K_M")
	preview, err := Parse(context.Background(), "resume.docx", docx, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Profile.Achievements) != 1 {
		t.Fatalf("contest not classified as achievement: %+v", preview.Profile)
	}
	achievement := preview.Profile.Achievements[0]
	if !strings.Contains(strings.ToLower(achievement.Result), "участ") || strings.Contains(strings.ToLower(achievement.Result), "побед") {
		t.Fatal(achievement)
	}
	if len(preview.Profile.Education) != 1 || strings.Contains(strings.ToLower(preview.Profile.Education[0].University), "иннополис") || strings.Contains(strings.ToLower(preview.Profile.Education[0].Degree), "конкурс") {
		t.Fatal(preview.Profile.Education)
	}
	t.Logf("Education=%+v; achievement=%+v", preview.Profile.Education, achievement)
}
