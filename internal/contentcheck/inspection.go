package contentcheck

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Inspection views are evidence for the reviewer, never replacement output.
// Work and decoding depth are bounded; no links or decoded instructions execute.
func inspectionViews(text string) ([]string, error) {
	values := []string{text}
	if json.Valid([]byte(text)) {
		if err := ValidateJSON(text); err != nil {
			return nil, err
		}
		var v any
		json.Unmarshal([]byte(text), &v)
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case string:
				values = append(values, x)
			case []any:
				for _, v := range x {
					walk(v)
				}
			case map[string]any:
				for k, v := range x {
					values = append(values, k)
					walk(v)
				}
			}
		}
		walk(v)
	}
	var views []string
	seen := map[string]bool{}
	budget := 0
	add := func(label, s string) error {
		if seen[s] || s == text || strings.TrimSpace(s) == "" {
			return nil
		}
		seen[s] = true
		if len([]rune(s)) > 24000 || budget+len([]rune(s)) > 12000 {
			return fmt.Errorf("слишком много замаскированного текста; загрузите обычный читаемый текст")
		}
		budget += len([]rune(s))
		views = append(views, label+": "+s)
		return nil
	}
	for _, value := range values {
		for _, r := range value {
			if forbiddenRune(r) {
				return nil, fmt.Errorf("удалите скрытые управляющие или невидимые символы из документа")
			}
		}
		// NFKC does not implement cross-script confusable detection; use an
		// additional labelled heuristic view, not an asserted canonical spelling.
		normalized := norm.NFKC.String(value)
		if normalized != value {
			if err := add("Unicode NFKC", normalized); err != nil {
				return nil, err
			}
		}
		for _, line := range strings.Split(normalized, "\n") {
			candidate := wordPattern.ReplaceAllStringFunc(line, func(word string) string {
				hasCyr := false
				for _, r := range word {
					if unicode.In(r, unicode.Cyrillic) {
						hasCyr = true
					}
				}
				if !hasCyr {
					return word
				}
				return strings.Map(func(r rune) rune {
					if mapped, ok := cyrillicLookalikes[r]; ok {
						return mapped
					}
					return r
				}, word)
			})
			// Short separated letters can hide words. The original remains available
			// so technology names and innocent lists are not treated as violations.
			candidate = spacedLetters.ReplaceAllStringFunc(candidate, func(s string) string {
				return strings.Map(func(r rune) rune {
					if unicode.IsSpace(r) || strings.ContainsRune("._*-", r) {
						return -1
					}
					return r
				}, s)
			})
			if candidate != line {
				if err := add("Возможное прочтение, требуется контекст", candidate); err != nil {
					return nil, err
				}
			}
		}
		current := value
		for depth := 0; depth < 2; depth++ {
			next := html.UnescapeString(current)
			if decoded, err := url.PathUnescape(next); err == nil {
				next = decoded
			}
			if next == current {
				break
			}
			for _, r := range next {
				if forbiddenRune(r) {
					return nil, fmt.Errorf("скрытые символы в закодированном тексте")
				}
			}
			if err := add("Декодированное представление", next); err != nil {
				return nil, err
			}
			current = next
		}
		for _, token := range encodedToken.FindAllString(value, 32) {
			if len(token) > 2048 {
				return nil, fmt.Errorf("слишком длинный закодированный фрагмент; используйте читаемый текст")
			}
			candidates := [][]byte{}
			if b, err := base64.StdEncoding.DecodeString(token); err == nil {
				candidates = append(candidates, b)
			}
			if b, err := base64.RawURLEncoding.DecodeString(token); err == nil {
				candidates = append(candidates, b)
			}
			if b, err := hex.DecodeString(token); err == nil {
				candidates = append(candidates, b)
			}
			for _, b := range candidates {
				s := string(b)
				if !utf8.Valid(b) || len([]rune(s)) < 8 {
					continue
				}
				readable := true
				letters := 0
				for _, r := range s {
					if forbiddenRune(r) {
						readable = false
						break
					}
					if unicode.IsLetter(r) {
						letters++
					}
				}
				if readable && letters*2 >= len([]rune(s)) {
					if err := add("Возможный декодированный текст", s); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return views, nil
}
func forbiddenRune(r rune) bool {
	return unicode.Is(unicode.Cf, r) || (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') || r == 0x034f || r == 0x115f || r == 0x1160 || r == 0x3164 || r == 0xffa0 || (r >= 0xfe00 && r <= 0xfe0f) || (r >= 0xe0100 && r <= 0xe01ef)
}

var wordPattern = regexp.MustCompile(`[\pL\pN@!$]+`)
var spacedLetters = regexp.MustCompile(`(?:\pL[\s._*\-]+){3,}\pL`)
var encodedToken = regexp.MustCompile(`[A-Za-z0-9_+/=-]{16,}`)
var cyrillicLookalikes = map[rune]rune{'a': 'а', 'A': 'А', 'e': 'е', 'E': 'Е', 'o': 'о', 'O': 'О', 'p': 'р', 'P': 'Р', 'c': 'с', 'C': 'С', 'x': 'х', 'X': 'Х', 'y': 'у', 'Y': 'У', 'k': 'к', 'K': 'К', 'm': 'м', 'M': 'М', 't': 'т', 'T': 'Т', '0': 'о', '3': 'е', '4': 'ч', '6': 'б', '@': 'а'}
