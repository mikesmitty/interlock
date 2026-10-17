package automation

import "os"

func writeText(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
