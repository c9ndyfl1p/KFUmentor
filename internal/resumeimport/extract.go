package resumeimport

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const MaxFileBytes = 10 << 20
const MaxTextRunes = 24000
const maxXMLBytes = 8 << 20

func ExtractText(name string, data []byte) (text string, err error) {
	// PDF parsers can panic on malformed inputs; a bad document must not take
	// down the local control panel.
	defer func() {
		if recover() != nil {
			text = ""
			err = fmt.Errorf("не удалось разобрать повреждённый документ")
		}
	}()
	if len(data) == 0 || len(data) > MaxFileBytes {
		return "", fmt.Errorf("размер файла должен быть от 1 байта до 10 МБ")
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		text, err = extractPDF(data)
	case ".docx":
		text, err = extractDOCX(data)
	case ".doc":
		return "", fmt.Errorf("старый формат DOC не поддерживается: сохраните файл в Word как DOCX или PDF")
	default:
		return "", fmt.Errorf("выберите PDF, DOCX или JSON")
	}
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\x00", ""))
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("не удалось прочитать кодировку текста; сохраните документ заново как PDF или DOCX")
	}
	if len([]rune(text)) < 20 {
		return "", fmt.Errorf("в документе не найден текст. Для PDF-скана нужно распознавание OCR; пока загрузите DOCX или PDF с выделяемым текстом")
	}
	if len([]rune(text)) > MaxTextRunes {
		return "", fmt.Errorf("в резюме больше 24 000 символов; загрузите более короткую версию — текст не был обрезан")
	}
	return text, nil
}

func extractPDF(data []byte) (string, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-")) {
		return "", fmt.Errorf("файл не является PDF")
	}
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("не удалось открыть PDF; проверьте файл и снимите пароль")
	}
	if r.NumPage() > 20 {
		return "", fmt.Errorf("поддерживаются резюме до 20 страниц")
	}
	// Extract page by page to retain source order and stop oversized documents.
	var text strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		part, err := pdfPageText(page)
		if err != nil {
			return "", fmt.Errorf("не удалось прочитать страницу %d PDF: %w", i, err)
		}
		text.WriteString(part)
		text.WriteString("\n")

		if text.Len() > MaxTextRunes*4 {
			return "", fmt.Errorf("слишком много текста в PDF; загрузите более короткую версию")
		}
	}
	return text.String(), nil
}

// Reconstruct physical lines from glyph positions. Plain stream extraction
// concatenates text on separate Td/Tm lines in many real PDFs.
func pdfPageText(page pdf.Page) (string, error) {
	glyphs := page.Content().Text
	if len(glyphs) > MaxTextRunes*2 {
		return "", fmt.Errorf("слишком много текста")
	}
	type row struct {
		y      float64
		glyphs []pdf.Text
	}
	var rows []row
	for _, glyph := range glyphs {
		if glyph.S == "" {
			continue
		}
		index := -1
		tolerance := math.Max(1, math.Abs(glyph.FontSize)*0.15)
		for i := range rows {
			if math.Abs(rows[i].y-glyph.Y) <= tolerance {
				index = i
				break
			}
		}
		if index < 0 {
			rows = append(rows, row{y: glyph.Y})
			index = len(rows) - 1
		}
		rows[index].glyphs = append(rows[index].glyphs, glyph)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].y > rows[j].y })
	var output strings.Builder
	for _, row := range rows {
		sort.SliceStable(row.glyphs, func(i, j int) bool { return row.glyphs[i].X < row.glyphs[j].X })
		var previous pdf.Text
		for i, glyph := range row.glyphs {
			gap := glyph.X - (previous.X + previous.W)
			if i > 0 && gap > math.Max(1, math.Abs(glyph.FontSize)*0.18) && !strings.HasSuffix(previous.S, " ") && !strings.HasPrefix(glyph.S, " ") {
				if gap > math.Abs(glyph.FontSize)*4 {
					output.WriteString("\t")
				} else {
					output.WriteString(" ")
				}
			}
			output.WriteString(glyph.S)
			previous = glyph
		}
		output.WriteString("\n")
	}
	return output.String(), nil
}

func extractDOCX(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("файл не является корректным DOCX")
	}
	parts := map[string]*zip.File{}
	var expanded uint64
	for _, f := range r.File {
		if f.Name == "word/document.xml" || regexp.MustCompile(`^word/(header|footer)\d+\.xml$`).MatchString(f.Name) {
			if f.UncompressedSize64 > maxXMLBytes {
				return "", fmt.Errorf("слишком большой текстовый раздел DOCX")
			}
			expanded += f.UncompressedSize64
			if expanded > maxXMLBytes {
				return "", fmt.Errorf("слишком большой текстовый раздел DOCX")
			}
			if parts[f.Name] != nil {
				return "", fmt.Errorf("повреждённая структура DOCX")
			}
			parts[f.Name] = f
		}
	}
	if parts["word/document.xml"] == nil {
		return "", fmt.Errorf("в DOCX отсутствует основной текст")
	}
	var text strings.Builder
	// Headers and footers often contain the candidate's name or contacts.
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "word/header") || parts[f.Name] == nil {
			continue
		}
		part, err := readXMLPart(f)
		if err != nil {
			return "", err
		}
		text.WriteString(part + "\n")
	}
	part, err := readXMLPart(parts["word/document.xml"])
	if err != nil {
		return "", err
	}
	text.WriteString(part)
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "word/footer") || parts[f.Name] == nil {
			continue
		}
		part, err := readXMLPart(f)
		if err != nil {
			return "", err
		}
		text.WriteString("\n" + part)
	}
	return text.String(), nil
}

func readXMLPart(f *zip.File) (string, error) {
	r, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("не удалось прочитать DOCX")
	}
	defer r.Close()
	decoder := xml.NewDecoder(io.LimitReader(r, maxXMLBytes+1))
	var text strings.Builder
	deleted := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("повреждённый XML в DOCX")
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local == "del" {
				deleted++
			}
			if deleted > 0 {
				continue
			}
			switch element.Name.Local {
			case "t":
				var value string
				if err := decoder.DecodeElement(&value, &element); err != nil {
					return "", fmt.Errorf("повреждённый текст DOCX")
				}
				text.WriteString(value)
			case "tab":
				text.WriteString("\t")
			case "br", "cr":
				text.WriteString("\n")
			}
		case xml.EndElement:
			if element.Name.Local == "del" {
				deleted--
			}
			if deleted == 0 && (element.Name.Local == "p" || element.Name.Local == "tr") {
				text.WriteString("\n")
			}
		}
	}
	return text.String(), nil
}
