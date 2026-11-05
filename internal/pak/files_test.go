package pak

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCollectPreflight(t *testing.T) {
	directory := t.TempDir()
	names := []string{"z", "a", "output.tar"}

	for _, name := range names {
		err := os.WriteFile(filepath.Join(directory, name), []byte("123"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	output := filepath.Join(directory, "output.tar")

	entries, total, err := collect(context.Background(), []string{directory}, output)
	if err != nil {
		t.Fatal(err)
	}

	actual := make([]string, 0, len(entries))

	for index := range entries {
		actual = append(actual, entries[index].header.Name)
	}

	base := filepath.Base(directory)

	expected := []string{base, base + "/a", base + "/z"}
	if !slices.Equal(actual, expected) || total != 6 {
		t.Fatalf("order/output exclusion/total: %v, %d", actual, total)
	}

	_, _, err = collect(context.Background(), []string{directory, directory}, output)
	if err == nil {
		t.Fatal("overlapping roots accepted")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = collect(ctx, []string{directory}, output)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
