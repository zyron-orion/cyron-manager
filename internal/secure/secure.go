package secure

import "strings"

var knownPrefixes = []string{"dpapi:", "aes:"}

func IsProtected(value string) bool {
	for _, prefix := range knownPrefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
