package pak

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/native"
)

type wimTestFormat struct {
	name  string
	level int
	codec uint32
}

var wimTestFormats = []wimTestFormat{
	{name: "wim", level: 1, codec: 0x20000},
	{name: "wim", level: 9, codec: 0x40000},
	{name: "esd", level: 3, codec: 0x40000},
}

func TestWIMRoundTrip(t *testing.T) {
	for _, test := range wimTestFormats {
		t.Run(test.name+strconv.Itoa(test.level), func(t *testing.T) {
			base := t.TempDir()
			first := filepath.Join(base, "first")
			second := filepath.Join(base, "second")

			mustWIMDirectory(t, first)
			mustWIMDirectory(t, second)
			mustWIMDirectory(t, filepath.Join(first, "empty-directory"))

			random := make([]byte, 200000)

			generator := rand.NewChaCha8([32]byte{1})
			_, _ = generator.Read(random)

			files := map[string][]byte{
				"first/empty":                       nil,
				"first/small":                       []byte("small payload"),
				"first/large":                       bytes.Repeat([]byte("WIM interoperability\n"), 100000),
				"first/duplicate":                   bytes.Repeat([]byte("WIM interoperability\n"), 100000),
				"second/\u00e9\u65e5\U0001f600.txt": random,
			}

			for name, data := range files {
				mustWIMFile(t, filepath.Join(base, filepath.FromSlash(name)), data)
			}

			if runtime.GOOS != "windows" {
				err := os.Link(filepath.Join(first, "small"), filepath.Join(first, "hard"))
				if err != nil {
					t.Fatal(err)
				}

				err = os.Symlink("small", filepath.Join(first, "link"))
				if err != nil {
					t.Fatal(err)
				}
			}

			format, _ := ParseFormat(test.name)
			options := Options{Format: format, Output: "-", Level: test.level, Threads: 2, Memory: 2 << 30}

			var archive bytes.Buffer

			err := Pack(t.Context(), []string{first, second}, options, &archive)
			if err != nil {
				t.Fatal(err)
			}

			encoded := archive.Bytes()
			if string(encoded[:8]) != "MSWIM\x00\x00\x00" || binary.LittleEndian.Uint32(encoded[16:])&test.codec == 0 {
				t.Fatal("unexpected WIM compression header")
			}

			if test.name == "esd" && binary.LittleEndian.Uint32(encoded[12:]) != 0xe00 {
				t.Fatal("ESD is not solid WIM")
			}

			options.Format = Format{}
			options.Output = filepath.Join(base, "out")

			err = Unpack(t.Context(), "-", options, bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}

			for name, expected := range files {
				actual, readErr := os.ReadFile(filepath.Join(options.Output, filepath.FromSlash(name)))
				if readErr != nil || sha256.Sum256(actual) != sha256.Sum256(expected) {
					t.Fatalf("payload %s: %v", name, readErr)
				}
			}

			if runtime.GOOS != "windows" {
				link, linkErr := os.Readlink(filepath.Join(options.Output, "first/link"))
				if linkErr != nil || link != "small" {
					t.Fatalf("symlink: %q, %v", link, linkErr)
				}

				original, statErr := os.Stat(filepath.Join(options.Output, "first/small"))
				if statErr != nil {
					t.Fatal(statErr)
				}

				hard, statErr := os.Stat(filepath.Join(options.Output, "first/hard"))
				if statErr != nil || !os.SameFile(original, hard) {
					t.Fatalf("hardlink: %v", statErr)
				}
			}

			err = Unpack(t.Context(), "-", options, bytes.NewReader(encoded))
			if err == nil {
				t.Fatal("existing output accepted")
			}
		})
	}
}

func TestWIMRejects(t *testing.T) {
	input := filepath.Join(t.TempDir(), "payload")
	mustWIMFile(t, input, bytes.Repeat([]byte("payload"), 1000))

	format, _ := ParseFormat("wim")

	var archive bytes.Buffer

	err := Pack(t.Context(), []string{input}, Options{Format: format, Output: "-", Level: 1, Threads: 1, Memory: 256 << 20}, &archive)
	if err != nil {
		t.Fatal(err)
	}

	encoded := archive.Bytes()

	corrupt := bytes.Clone(encoded)
	corrupt[208] ^= 1

	cases := [][]byte{corrupt, encoded[:len(encoded)-1], encoded[:100]}

	for _, data := range cases {
		output := t.TempDir()

		err = Unpack(t.Context(), "-", Options{Format: format, Output: output, Memory: 256 << 20}, bytes.NewReader(data))
		if err == nil {
			t.Fatal("corrupt or truncated WIM accepted")
		}

		assertWIMEmpty(t, output)
	}

	limits := []ExtractionLimits{{MaxEntries: 1}, {MaxOutput: 1}, {MaxFileSize: 1}}

	for _, limit := range limits {
		output := t.TempDir()

		err = Unpack(t.Context(), "-", Options{Format: format, Output: output, Limits: &limit, Memory: 256 << 20}, bytes.NewReader(encoded))
		if !errors.Is(err, ErrExtractionLimit) {
			t.Fatalf("limit: %v", err)
		}

		assertWIMEmpty(t, output)
	}

	indices := []int{-1, 2}

	for _, image := range indices {
		err = Unpack(t.Context(), "-", Options{Format: format, Output: t.TempDir(), Image: image, Memory: 256 << 20}, bytes.NewReader(encoded))
		if err == nil || !strings.Contains(err.Error(), "image") {
			t.Fatalf("image selection: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	output := filepath.Join(t.TempDir(), "cancel.wim")

	err = Pack(ctx, []string{input}, Options{Format: format, Output: output, Level: 1}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}

	_, err = os.Stat(output)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled output published: %v", err)
	}

	err = Pack(t.Context(), []string{input}, Options{Format: format, Output: output, Level: 9, Memory: 1}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "one worker") {
		t.Fatalf("memory budget: %v", err)
	}

	assertWIMEmpty(t, filepath.Dir(output))
}

func TestWIMPaths(t *testing.T) {
	paths := []string{"../escape", "/absolute", "a/../b", "a//b", "C:/escape", "a\\b", "NUL", "name.", "file:stream"}

	for _, name := range paths {
		_, err := wimPath("/"+name, path.Base(name))
		if err == nil {
			t.Errorf("unsafe path accepted: %q", name)
		}
	}

	_, err := wimHeader(native.WIMEntry{Path: "/device", Name: "device", Mode: 0o020600})
	if err == nil {
		t.Fatal("device accepted")
	}
}

func TestWIMImagexInterop(t *testing.T) {
	imagex := os.Getenv("PAKR_WIMLIB_IMAGEX")
	if imagex == "" {
		t.Skip("set PAKR_WIMLIB_IMAGEX to the official wimlib 1.14.5 imagex executable")
	}

	for _, test := range wimTestFormats {
		t.Run(test.name+strconv.Itoa(test.level), func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "source")
			mustWIMDirectory(t, source)

			payload := bytes.Repeat([]byte("interoperable data\n"), 10000)
			mustWIMFile(t, filepath.Join(source, "payload"), payload)

			format, _ := ParseFormat(test.name)
			archive := filepath.Join(base, "pakr."+test.name)

			err := Pack(t.Context(), []string{source}, Options{Format: format, Output: archive, Level: test.level, Threads: 2, Memory: 2 << 30}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			runWIMImagex(t, imagex, "verify", archive)

			applied := filepath.Join(base, "applied")
			runWIMImagex(t, imagex, "apply", archive, "1", applied, "--no-acls")

			actual, err := os.ReadFile(filepath.Join(applied, "source/payload"))
			if err != nil || !bytes.Equal(actual, payload) {
				t.Fatalf("imagex apply: %v", err)
			}

			reference := filepath.Join(base, "reference."+test.name)
			compression := "LZX"

			if test.level == 1 {
				compression = "XPRESS"
			}

			arguments := []string{"capture", source, reference, "first", "--no-acls", "--check", "--compress=" + compression}

			if test.name == "esd" {
				arguments = append(arguments, "--solid", "--solid-compress=LZMS", "--solid-chunk-size=64M")
			}

			runWIMImagex(t, imagex, arguments...)

			out := filepath.Join(base, "reference-out")

			err = Unpack(t.Context(), reference, Options{Output: out, Memory: 2 << 30}, nil)
			if err != nil {
				t.Fatal(err)
			}

			actual, err = os.ReadFile(filepath.Join(out, "payload"))
			if err != nil || !bytes.Equal(actual, payload) {
				t.Fatalf("imagex capture: %v", err)
			}

			runWIMImagex(t, imagex, "append", source, reference, "second", "--no-acls")

			err = Unpack(t.Context(), reference, Options{Output: t.TempDir(), Memory: 2 << 30}, nil)
			if err == nil || !strings.Contains(err.Error(), "--image") {
				t.Fatalf("multiple images require selection: %v", err)
			}

			err = Unpack(t.Context(), reference, Options{Output: t.TempDir(), Image: 2, Memory: 2 << 30}, nil)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWIMRootAndRollback(t *testing.T) {
	source := t.TempDir()
	mustWIMFile(t, filepath.Join(source, "data"), []byte("payload"))

	t.Chdir(source)

	format, _ := ParseFormat("wim")
	archive := filepath.Join(source, "archive.wim")

	err := Pack(t.Context(), []string{"."}, Options{Format: format, Output: archive, Level: 3}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	destination := t.TempDir()

	err = Unpack(t.Context(), archive, Options{Output: destination}, nil)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(destination, "data"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("root contents: %q, %v", data, err)
	}

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	defer root.Close()

	items := []wimItem{
		{header: native.Header{Name: "created", Kind: native.Directory, Mode: 0o755}},
		{header: native.Header{Name: "created/empty", Kind: native.Regular, Mode: 0o644}},
		{header: native.Header{Name: "created/missing", Kind: native.Regular, Size: 1}, size: 1},
	}

	copying := copier{ctx: t.Context(), buffer: make([]byte, native.BufferSize)}
	extract := extractor{root: root, copying: &copying}
	stage := wimStage{directory: t.TempDir()}

	err = publishWIM(items, &stage, &extract)
	if err == nil {
		t.Fatal("missing staged resource accepted")
	}

	_, err = root.Lstat("created")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication was not rolled back: %v", err)
	}
}

func TestWIMImagexUnsafeLinks(t *testing.T) {
	executable := os.Getenv("PAKR_WIMLIB_IMAGEX")
	if executable == "" || runtime.GOOS == "windows" {
		t.Skip("requires official imagex and Unix symlink capture")
	}

	targets := []string{"../../outside", "/outside"}

	for index, target := range targets {
		source := t.TempDir()

		err := os.Symlink(target, filepath.Join(source, "link"))
		if err != nil {
			t.Fatal(err)
		}

		archive := filepath.Join(t.TempDir(), strconv.Itoa(index)+".wim")
		runWIMImagex(t, executable, "capture", source, archive, "--no-acls", "--norpfix")

		output := t.TempDir()

		err = Unpack(t.Context(), archive, Options{Output: output}, nil)
		if err == nil {
			t.Fatalf("accepted unsafe link %q", target)
		}

		assertWIMEmpty(t, output)
	}
}

func TestWIMRejectsUnsupportedInputs(t *testing.T) {
	source := t.TempDir()
	filename := filepath.Join(source, "data")
	mustWIMFile(t, filename, []byte("payload"))

	if runtime.GOOS == "windows" {
		mustWIMFile(t, filename+":alternate", []byte("alternate contents"))
	} else {
		other := filepath.Join(source, "DATA")
		mustWIMFile(t, other, []byte("case collision"))

		original, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}

		collision, err := os.Stat(other)
		if err != nil {
			t.Fatal(err)
		}

		if os.SameFile(original, collision) {
			t.Skip("filesystem cannot represent case-colliding inputs")
		}
	}

	format, _ := ParseFormat("wim")
	output := filepath.Join(t.TempDir(), "archive.wim")

	err := Pack(t.Context(), []string{source}, Options{Format: format, Output: output, Level: 3}, nil)
	if err == nil {
		t.Fatal("unsupported input accepted")
	}

	_, err = os.Lstat(output)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed capture published output: %v", err)
	}
}

func TestWIMCancellationAndWorkers(t *testing.T) {
	session, err := native.NewWIM(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	_, err = native.NewWIM(ctx)
	session.Close()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting job cancellation: %v", err)
	}

	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()

	session, err = native.NewWIM(ctx)
	if err != nil {
		t.Fatal(err)
	}

	defer session.Close()

	err = session.Create(native.WIMLZX, 100)
	if err != nil {
		t.Fatal(err)
	}

	source := t.TempDir()
	mustWIMFile(t, filepath.Join(source, "data"), bytes.Repeat([]byte("native cancellation"), 100000))

	sources := []native.WIMSource{{Path: source, Target: "/"}}

	err = session.Capture(sources)
	if err != nil {
		t.Fatal(err)
	}

	session.Progress = func(completed uint64, workers int) {
		cancel()
	}

	err = session.Write(filepath.Join(t.TempDir(), "cancel.wim"), false, 2)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("native callback cancellation: %v", err)
	}

	_, minimum, err := wimWorkers(native.WIMLZMS, 180, 64<<20, 1, 32<<20, 16<<30)
	if err != nil {
		t.Fatal(err)
	}

	workers, _, err := wimWorkers(native.WIMLZMS, 180, 64<<20, 8, 32<<20, minimum)
	if err != nil || workers != 1 {
		t.Fatalf("memory must reduce workers: %d, %v", workers, err)
	}

	_, _, err = wimWorkers(native.WIMLZMS, 180, 64<<20, 8, 32<<20, minimum-1)
	if err == nil {
		t.Fatal("insufficient memory accepted")
	}
}

func TestWIMDirectorySymlinkOrder(t *testing.T) {
	probe := t.TempDir()

	err := os.Symlink("target", filepath.Join(probe, "link"))
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}

		t.Fatal(err)
	}

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	defer root.Close()

	copying := copier{ctx: t.Context(), buffer: make([]byte, native.BufferSize)}
	extract := extractor{root: root, copying: &copying}

	items := []wimItem{
		{header: native.Header{Name: "a-link", Kind: native.Symlink, Link: "z-target"}},
		{header: native.Header{Name: "z-target", Kind: native.Directory, Mode: 0o755}},
	}

	err = publishWIM(items, &wimStage{}, &extract)
	if err != nil {
		t.Fatal(err)
	}

	info, err := root.Stat("a-link")
	if err != nil || !info.IsDir() {
		t.Fatalf("directory symlink was not preserved: %v", err)
	}
}

func mustWIMDirectory(t *testing.T, name string) {
	t.Helper()

	err := os.MkdirAll(name, 0o755)
	if err != nil {
		t.Fatal(err)
	}
}

func mustWIMFile(t *testing.T, name string, data []byte) {
	t.Helper()

	err := os.WriteFile(name, data, 0o640)
	if err != nil {
		t.Fatal(err)
	}
}

func assertWIMEmpty(t *testing.T, output string) {
	t.Helper()

	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed extraction left entries: %v, %v", entries, err)
	}
}

func runWIMImagex(t *testing.T, executable string, arguments ...string) {
	t.Helper()

	command := exec.CommandContext(t.Context(), executable, arguments...)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("imagex %v: %v\n%s", arguments, err, output)
	}
}
