package download

import (
	"testing"

	"cyronmanager/internal/types"
)

func TestParseScheduleTime(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"00:00", 0, true},
		{"07:30", 450, true},
		{"23:59", 1439, true},
		{" 07:30 ", 450, true},
		{"24:00", 0, false},
		{"07:60", 0, false},
		{"7:30", 0, false},
		{"0730", 0, false},
		{"aa:bb", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseScheduleTime(c.in)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("parseScheduleTime(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestScheduleWindowActive(t *testing.T) {
	cases := []struct {
		name          string
		minutes       int
		startM, stopM int
		want          bool
	}{
		{"inside normal window", 600, 480, 1020, true},
		{"before normal window", 400, 480, 1020, false},
		{"after normal window", 1100, 480, 1020, false},
		{"start boundary inclusive", 480, 480, 1020, true},
		{"stop boundary exclusive", 1020, 480, 1020, false},
		{"late night window after midnight edge", 120, 1320, 300, true},
		{"late night window before start", 600, 1320, 300, false},
		{"late night window at 23:59", 1439, 1320, 300, true},
		{"late night window at 00:00", 0, 1320, 300, true},
		{"late night window at stop boundary", 300, 1320, 300, false},
	}
	for _, c := range cases {
		if got := scheduleWindowActive(c.minutes, c.startM, c.stopM); got != c.want {
			t.Errorf("%s: scheduleWindowActive(%d, %d, %d) = %v, want %v", c.name, c.minutes, c.startM, c.stopM, got, c.want)
		}
	}
}

func TestScheduleActiveGuards(t *testing.T) {
	base := types.Settings{ScheduleEnabled: true, ScheduleStart: "22:00", ScheduleStop: "06:00"}

	if scheduleActive(types.Settings{}) != true {
		t.Error("disabled schedule must always be active")
	}
	invalid := base
	invalid.ScheduleStart = "99:99"
	if scheduleActive(invalid) != true {
		t.Error("invalid times must fall back to always active")
	}
	equal := base
	equal.ScheduleStop = "22:00"
	if scheduleActive(equal) != true {
		t.Error("equal start/stop must fall back to always active")
	}
}

func TestIsLocalTorrentFile(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"C:\\Downloads\\ubuntu.torrent", true},
		{"FILE.TORRENT", true},
		{"/tmp/a.torrent", true},
		{"", false},
		{"magnet:?xt=urn:btih:abc", false},
		{"https://example.com/a.torrent", false},
		{"http://example.com/a.torrent", false},
		{"https://example.com/a.zip", false},
	}
	for _, c := range cases {
		if got := isLocalTorrentFile(c.in); got != c.want {
			t.Errorf("isLocalTorrentFile(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
