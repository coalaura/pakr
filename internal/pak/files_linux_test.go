package pak

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPackingRejectsSubstitutedFIFO(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "input")

	err := os.WriteFile(filename, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	entries, _, err := collect(t.Context(), []string{filename}, "")
	if err != nil {
		t.Fatal(err)
	}

	err = os.Remove(filename)
	if err != nil {
		t.Fatal(err)
	}

	err = unix.Mkfifo(filename, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	copying := copier{ctx: t.Context(), buffer: make([]byte, 1024)}

	err = copying.file(io.Discard, &entries[0])
	if err == nil {
		t.Fatal("substituted FIFO accepted")
	}
}
