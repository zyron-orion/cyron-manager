package proxy

import "strings"

func CleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		name := item
		if index := strings.IndexByte(item, '='); index >= 0 {
			name = item[:index]
		}
		switch strings.ToUpper(strings.TrimSpace(name)) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, item)
	}
	return out
}
