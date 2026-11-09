package pak

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

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

func TestFileTimeWithoutEmptyPath(t *testing.T) {
	if os.Getenv("PAKR_TEST_FILETIME_SECCOMP") == "" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}

		command := exec.Command(executable, "-test.run=^TestFileTimeWithoutEmptyPath$", "-test.v")
		command.Env = append(os.Environ(), "PAKR_TEST_FILETIME_SECCOMP=1")

		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("older utimensat ABI subprocess: %v\n%s", err, output)
		}

		t.Logf("%s", output)

		return
	}

	file, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	// Restrict this thread in a disposable subprocess. Reject the newer flag
	// exactly as older kernels do, while allowing the original fd-based ABI.
	runtime.LockOSThread()

	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.SYS_UTIMENSAT, Jf: 3},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 40},
		{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: unix.AT_EMPTY_PATH, Jf: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EINVAL)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}

	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}

	err = unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// prctl copies the filter synchronously; keep both Go objects alive until it returns.
	err = unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0)

	runtime.KeepAlive(program)
	runtime.KeepAlive(filters)

	if err != nil {
		t.Fatal(err)
	}

	value := unix.Timespec{Sec: 1700000000, Nsec: 123456789}
	times := []unix.Timespec{value, value}

	err = unix.UtimesNanoAt(int(file.Fd()), "", times, unix.AT_EMPTY_PATH)
	if !errors.Is(err, unix.EINVAL) {
		t.Fatalf("AT_EMPTY_PATH was not rejected: %v", err)
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
