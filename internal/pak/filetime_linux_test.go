package pak

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileTimeNanoseconds(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	assertFileTimeNanoseconds(t, file)
}

func TestFileTimeWithoutProcfs(t *testing.T) {
	directory := os.Getenv("PAKR_TEST_FILETIME_ROOT")
	if directory == "" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}

		command := exec.Command(executable, "-test.run=^TestFileTimeWithoutProcfs$", "-test.v")
		command.Env = append(os.Environ(), "PAKR_TEST_FILETIME_ROOT="+t.TempDir())

		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("procfs-free subprocess: %v\n%s", err, output)
		}

		t.Logf("%s", output)

		return
	}

	file, err := os.Create(filepath.Join(directory, "file"))
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	// Isolate the process root after opening the file; no procfs exists here.
	err = unix.Chroot(directory)
	if errors.Is(err, unix.EPERM) {
		t.Skip("chroot requires CAP_SYS_CHROOT")
	}

	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat("/proc/self/fd")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("procfs unexpectedly available: %v", err)
	}

	assertFileTimeNanoseconds(t, file)
}

func assertFileTimeNanoseconds(t *testing.T, file *os.File) {
	t.Helper()

	timestamps := []time.Time{time.Unix(1700000000, 123456789), time.Unix(-123, 987654321)}

	for _, modified := range timestamps {
		err := setFileTime(file, modified)
		if err != nil {
			t.Fatal(err)
		}

		var info unix.Stat_t

		err = unix.Fstat(int(file.Fd()), &info)
		if err != nil {
			t.Fatal(err)
		}

		if info.Mtim.Sec != modified.Unix() || info.Mtim.Nsec != int64(modified.Nanosecond()) || info.Atim != info.Mtim {
			t.Fatalf("timestamp precision lost: atime=%v mtime=%v, want %v", info.Atim, info.Mtim, modified)
		}
	}
}
