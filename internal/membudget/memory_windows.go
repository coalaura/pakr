package membudget

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type memoryStatus struct {
	length                   uint32
	load                     uint32
	totalPhysical            uint64
	availablePhysical        uint64
	totalPageFile            uint64
	availablePageFile        uint64
	totalVirtual             uint64
	availableVirtual         uint64
	availableExtendedVirtual uint64
}

var globalMemoryStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func detect() headroom {
	var (
		status memoryStatus
		limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	)

	status.length = uint32(unsafe.Sizeof(status))
	result, _, _ := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))

	err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil)

	return windowsHeadroom(status, result != 0, limits, err == nil)
}

func windowsHeadroom(status memoryStatus, statusOK bool, limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION, limitsOK bool) headroom {
	var room headroom

	if statusOK {
		room.include(status.availablePhysical)
		room.include(status.availablePageFile)
	}

	if limitsOK {
		// The query accepts a null handle for this process's job. Peak charged
		// memory is an upper bound on current use, giving conservative headroom
		// without relying on undocumented job-memory query classes.
		flags := limits.BasicLimitInformation.LimitFlags
		if flags&windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY != 0 {
			room.limit(uint64(limits.ProcessMemoryLimit), uint64(limits.PeakProcessMemoryUsed))
		}

		if flags&windows.JOB_OBJECT_LIMIT_JOB_MEMORY != 0 {
			room.limit(uint64(limits.JobMemoryLimit), uint64(limits.PeakJobMemoryUsed))
		}
	}

	return room
}
