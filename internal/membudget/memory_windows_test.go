package membudget

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsHeadroom(t *testing.T) {
	status := memoryStatus{availablePhysical: 8 << 30, availablePageFile: 2 << 30}

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_MEMORY},
		ProcessMemoryLimit:    512 << 20,
		JobMemoryLimit:        1 << 30,
		PeakProcessMemoryUsed: 128 << 20,
		PeakJobMemoryUsed:     768 << 20,
	}

	room := windowsHeadroom(status, true, limits, true)
	if !room.known || room.bytes != 256<<20 {
		t.Fatalf("job constraint: %+v", room)
	}

	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY

	room = windowsHeadroom(status, true, limits, true)
	if room.bytes != 384<<20 {
		t.Fatalf("process constraint: %+v", room)
	}

	room = windowsHeadroom(status, true, limits, false)
	if room.bytes != 2<<30 {
		t.Fatalf("commit constraint: %+v", room)
	}

	status.availablePhysical = 64 << 20

	room = windowsHeadroom(status, true, limits, true)
	if room.bytes != 64<<20 {
		t.Fatalf("physical constraint: %+v", room)
	}

	limits.PeakProcessMemoryUsed = limits.ProcessMemoryLimit + 1

	room = windowsHeadroom(status, false, limits, true)
	if !room.known || room.bytes != 0 {
		t.Fatalf("exhausted process limit: %+v", room)
	}

	room = windowsHeadroom(status, false, limits, false)
	if room.known {
		t.Fatalf("failed queries: %+v", room)
	}
}
