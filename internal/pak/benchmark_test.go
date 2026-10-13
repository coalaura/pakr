package pak

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func BenchmarkPack(b *testing.B) {
	directory := b.TempDir()
	source := filepath.Join(directory, "source")

	err := os.Mkdir(source, 0o755)
	if err != nil {
		b.Fatal(err)
	}

	data := make([]byte, 1<<20)

	random := rand.NewChaCha8([32]byte{7})

	for index := range data {
		if index%256 < 128 {
			data[index] = byte(random.Uint64())
		}
	}

	for index := range 32 {
		err = os.WriteFile(filepath.Join(source, strconv.Itoa(index)), data, 0o600)
		if err != nil {
			b.Fatal(err)
		}
	}

	formats := []string{"tar", "tar.gz", "tar.zst", "zip", "native-tar"}

	for _, name := range formats {
		b.Run(name, func(b *testing.B) {
			benchmarkPack(b, name, directory, source, int64(32*len(data)))
		})
	}
}

func benchmarkPack(b *testing.B, name, directory, source string, size int64) {
	output := filepath.Join(directory, "archive")
	options := Options{Output: output, Level: 3, Threads: 4}

	var err error

	if name == "native-tar" {
		_, err = exec.LookPath("tar")
		if err != nil {
			b.Skip("native tar is unavailable")
		}
	} else {
		options.Format, err = ParseFormat(name)
		if err != nil {
			b.Fatal(err)
		}
	}

	inputs := []string{source}

	b.SetBytes(size)
	b.ReportAllocs()

	for b.Loop() {
		err = os.Remove(output)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			b.Fatal(err)
		}

		if name == "native-tar" {
			command := exec.CommandContext(context.Background(), "tar", "-cf", output, "-C", directory, "source")

			err = command.Run()
		} else {
			err = Pack(context.Background(), inputs, options, io.Discard)
		}

		if err != nil {
			b.Fatal(err)
		}
	}

	info, err := os.Stat(output)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportMetric(float64(info.Size())/float64(size), "output/input")
}
