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
	root := r.Trailer().Key("Root")
	for _, key := range []string{"OpenAction", "AA", "AcroForm", "Names", "AF", "Collection", "OCProperties"} {
		if !root.Key(key).IsNull() {
			return "", fmt.Errorf("PDF содержит дополнительные объекты, которые нельзя полностью проверить; загрузите простое текстовое резюме без фото, форм и вложений")
		}
	}
	if r.NumPage() > 20 {
		return "", fmt.Errorf("поддерживаются резюме до 20 страниц")
	}
	// Extract page by page to retain source order and stop oversized documents.
	var text strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if len(page.Resources().Key("XObject").Keys()) > 0 {
			return "", fmt.Errorf("PDF содержит изображение или встроенный графический объект; загрузите текстовое резюме без фото и изображений")
		}
		for _, key := range []string{"AA", "AF", "PieceInfo"} {
			if !page.V.Key(key).IsNull() {
				return "", fmt.Errorf("PDF содержит непроверяемые дополнительные объекты; загрузите текстовое резюме без вложений")
			}
		}
		annotations := page.V.Key("Annots")
		for j := 0; j < annotations.Len(); j++ {
			annotation := annotations.Index(j)
			if annotation.Key("Subtype").Name() != "Link" || !annotation.Key("AA").IsNull() {
				return "", fmt.Errorf("PDF содержит аннотации или формы; загрузите текстовое резюме без дополнительных объектов")
			}
			action := annotation.Key("A")
			if !action.IsNull() {
				if action.Key("S").Name() != "URI" {
					return "", fmt.Errorf("PDF содержит непроверяемое действие ссылки")
				}
				text.WriteString("\nСсылка в резюме: " + action.Key("URI").Text() + "\n")
			}
		}
		if err := checkPDFOperators(page); err != nil {
			return "", err
		}
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
	if len(r.File) > 200 {
		return "", fmt.Errorf("слишком сложный DOCX; сохраните простое резюме без вложений")
	}
	var links strings.Builder
	for _, f := range r.File {
		if f.UncompressedSize64 > maxXMLBytes || expanded+f.UncompressedSize64 > maxXMLBytes {
			return "", fmt.Errorf("слишком большой распакованный DOCX")
		}
		expanded += f.UncompressedSize64
		// Verify actual expanded bytes and checksum, including parts not extracted.
		reader, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("повреждённый DOCX")
		}
		n, readErr := io.Copy(io.Discard, io.LimitReader(reader, maxXMLBytes+1))
		reader.Close()
		if readErr != nil || n > maxXMLBytes || uint64(n) != f.UncompressedSize64 {
			return "", fmt.Errorf("некорректный распакованный раздел DOCX")
		}

		if strings.HasSuffix(f.Name, ".rels") {
			part, err := docxLinks(f)
			if err != nil {
				return "", err
			}
			links.WriteString(part)
		}
		if !safeDOCXPart(f.Name) {
			return "", fmt.Errorf("DOCX содержит изображения или непроверяемые вложения/разделы; загрузите резюме без фото, изображений, комментариев и вложений")
		}
		if f.Name == "word/document.xml" || regexp.MustCompile(`^word/(header|footer)\d+\.xml$`).MatchString(f.Name) {
			if f.UncompressedSize64 > maxXMLBytes {
				return "", fmt.Errorf("слишком большой текстовый раздел DOCX")
			}
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
	text.WriteString(links.String())
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
	limited := &io.LimitedReader{R: r, N: maxXMLBytes + 1}
	decoder := xml.NewDecoder(limited)
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if limited.N <= 0 {
				return "", fmt.Errorf("превышен фактический объём XML DOCX")
			}
			break
		}
		if err != nil {
			return "", fmt.Errorf("повреждённый XML в DOCX")
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "del", "ins", "altChunk", "object", "drawing", "pict", "instrText", "fldSimple", "txbxContent":
				return "", fmt.Errorf("DOCX содержит исправления, поля или встроенные объекты; примите исправления и загрузите простое текстовое резюме без фото")
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
			if element.Name.Local == "p" || element.Name.Local == "tr" {
				text.WriteString("\n")
			}
		}
	}
	return text.String(), nil
}

// A small, explicit package subset prevents text extraction from silently
// dropping images, macros, attached documents, comments and additional stories.
func safeDOCXPart(name string) bool {
	switch name {
	case "[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "docProps/app.xml",
		"word/document.xml", "word/styles.xml", "word/stylesWithEffects.xml", "word/settings.xml", "word/webSettings.xml", "word/fontTable.xml", "word/numbering.xml":
		return true
	}
	return regexp.MustCompile(`^word/(header[0-9]+\.xml|footer[0-9]+\.xml|theme/theme[0-9]+\.xml|_rels/(document|header[0-9]+|footer[0-9]+|fontTable)\.xml\.rels)$`).MatchString(name)
}

func docxLinks(f *zip.File) (string, error) {
	r, err := f.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	limited := &io.LimitedReader{R: r, N: maxXMLBytes + 1}
	decoder := xml.NewDecoder(limited)
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if limited.N <= 0 {
				return "", fmt.Errorf("превышен фактический объём XML DOCX")
			}
			break
		}
		if err != nil {
			return "", fmt.Errorf("повреждённые связи DOCX")
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		var target, mode, kind string
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "Target":
				target = attr.Value
			case "TargetMode":
				mode = attr.Value
			case "Type":
				kind = attr.Value
			}
		}
		if mode == "External" {
			if !strings.HasSuffix(kind, "/hyperlink") {
				return "", fmt.Errorf("DOCX содержит внешнее изображение или связанный объект; загрузите резюме без фото и вложений")
			}
			text.WriteString("\nСсылка в резюме: " + target + "\n")
		}
	}
	return text.String(), nil
}

func checkPDFOperators(page pdf.Page) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("PDF содержит изображение или непроверяемый поток; загрузите текстовое резюме без фото и изображений")
		}
	}()
	stream := page.V.Key("Contents")
	if stream.IsNull() {
		return nil
	}
	pdf.Interpret(stream, func(stack *pdf.Stack, op string) {
		// Inline images have no XObject entry. Stop before their binary data is parsed.
		if op == "BI" || op == "Do" {
			panic("unsupported image or object")
		}
		for stack.Len() > 0 {
			stack.Pop()
		}
	})
	return nil
}
