package download

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyronmanager/internal/store"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

func TestMoveTaskInQueueReordersQueuedTasks(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	settings := types.Settings{DownloadDir: filepath.Join(dir, "downloads"), MaxConcurrent: 2, Connections: 4}
	m := New(context.Background(), st, tools.New(dir), settings, dir, func(string, any) {})
	defer m.Close()

	now := time.Now().UnixMilli()
	for index, id := range []string{"a", "b", "c"} {
		task := types.Task{
			ID: id, URL: "https://example.com/" + id, Title: id, Engine: "http", Kind: "file",
			Status: types.StatusQueued, Dir: settings.DownloadDir,
			CreatedAt: now + int64(index), UpdatedAt: now + int64(index),
		}
		if err := st.SaveTask(&task); err != nil {
			t.Fatal(err)
		}
	}

	queued, err := m.MoveTaskInQueue("c", "up")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 3 || queued[0].ID != "a" || queued[1].ID != "c" || queued[2].ID != "b" {
		t.Fatalf("unexpected order after move: %+v", queued)
	}

	queued, err = m.MoveTaskInQueue("c", "up")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 3 || queued[0].ID != "c" || queued[1].ID != "a" || queued[2].ID != "b" {
		t.Fatalf("unexpected order after second move: %+v", queued)
	}

	stored, err := st.GetTask("c")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Priority != 3 {
		t.Fatalf("priority = %d, want 3", stored.Priority)
	}

	queued, err = m.MoveTaskInQueue("c", "up")
	if err != nil {
		t.Fatal(err)
	}
	if queued[0].ID != "c" {
		t.Fatalf("boundary move changed order: %+v", queued)
	}
}

func TestDetectCategory(t *testing.T) {
	tests := []struct {
		name      string
		engine    string
		kind      string
		quality   string
		container string
		title     string
		want      string
	}{
		{name: "video extension", title: "movie.mkv", want: types.CategoryVideo},
		{name: "audio extension", title: "track.flac", want: types.CategoryAudio},
		{name: "archive extension", title: "release.zip", want: types.CategoryArchive},
		{name: "program extension", title: "setup.exe", want: types.CategoryProgram},
		{name: "torrent without extension", kind: "torrent", title: "linux-iso", want: types.CategoryOther},
		{name: "media audio quality", engine: "ytdlp", quality: "audio", title: "episode", want: types.CategoryAudio},
		{name: "media mp3 container", engine: "ytdlp", container: "mp3", title: "episode", want: types.CategoryAudio},
		{name: "media video", engine: "ytdlp", title: "episode", want: types.CategoryVideo},
		{name: "unknown", engine: "http", title: "readme", want: types.CategoryOther},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := detectCategory(test.engine, test.kind, test.quality, test.container, test.title)
			if got != test.want {
				t.Fatalf("detectCategory() = %q, want %q", got, test.want)
			}
		})
	}
}
