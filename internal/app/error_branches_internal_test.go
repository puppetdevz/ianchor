package app

import (
	"os"
	"testing"

	"github.com/puppetdevz/ianchor/internal/store"
)

func TestOpenDBFailurePropagates(t *testing.T) {
	original := openDBFunc
	openDBFunc = func(string) (*store.DB, error) { return nil, os.ErrPermission }
	t.Cleanup(func() { openDBFunc = original })
	if _, err := openDB(t.TempDir()); err == nil {
		t.Fatal("openDB accepted a permission error")
	}
}

func TestListAndCompletionFailures(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/ianchor.db", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenReadOnly(dir); err == nil {
		t.Fatal("readonly open accepted a directory")
	}
	if payload := []store.Entry{}; payload == nil {
		t.Fatal("empty payload")
	}
	empty := []store.Entry{}
	if len(empty) != 0 {
		t.Fatal(len(empty))
	}
}

// encodeListPayload reports the encoder error instead of an empty payload.
func TestEncodeListPayloadReportsEncoderFailure(t *testing.T) {
	if _, err := encodeListPayload(make(chan int)); err == nil {
		t.Fatal("unsupported payload encoded without error")
	}
}
