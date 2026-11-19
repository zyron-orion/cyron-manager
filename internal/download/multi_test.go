package download

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadWithIdleDetectsStall(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()
	result := make(chan error, 1)
	go func() {
		result <- readWithIdle(context.Background(), pr, 50*time.Millisecond, make([]byte, 64), func([]byte) error { return nil })
	}()
	select {
	case err := <-result:
		if !errors.Is(err, errDownloadStalled) {
			t.Fatalf("readWithIdle = %v, want errDownloadStalled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readWithIdle did not stop on an idle reader")
	}
}

func TestReadWithIdleDeliversDataUntilEOF(t *testing.T) {
	var received strings.Builder
	err := readWithIdle(context.Background(), strings.NewReader("hello"), time.Second, make([]byte, 16), func(chunk []byte) error {
		received.Write(chunk)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if received.String() != "hello" {
		t.Fatalf("received %q, want hello", received.String())
	}
}
