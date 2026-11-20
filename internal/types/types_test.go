package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskJSONOmitsManualCredentials(t *testing.T) {
	data, err := json.Marshal(Task{
		ID:             "task-1",
		Username:       "user",
		Password:       "pass",
		Cookies:        "sid=secret",
		BrowserCookies: "browser=secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"user", "pass", "sid=secret", "browser=secret", `"username"`, `"password"`, `"cookies"`} {
		if strings.Contains(string(data), value) {
			t.Fatalf("task JSON contains %q: %s", value, data)
		}
	}
}
