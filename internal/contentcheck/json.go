package contentcheck

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ValidateJSON rejects duplicate keys, which otherwise let parsers interpret
// the same moderation verdict or profile differently.
func ValidateJSON(text string) error {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var read func(int) error
	read = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("слишком глубокий JSON")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					s, ok := key.(string)
					if !ok || keys[s] {
						return fmt.Errorf("повторяющиеся ключи JSON")
					}
					keys[s] = true
					if err := read(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := read(depth + 1); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("неожиданный разделитель JSON")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := read(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("ожидался один JSON")
	}
	return nil
}
