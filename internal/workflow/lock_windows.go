//go:build windows

package workflow

import "fmt"

func lockFile(path string) (func(), error) {
	return nil, fmt.Errorf("очередь откликов пока поддерживается на macOS и Linux")
}
