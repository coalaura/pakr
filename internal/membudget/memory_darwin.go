package membudget

/*
#include <mach/mach.h>
#include <sys/resource.h>
#include <sys/sysctl.h>

typedef struct {
	uint64_t available;
	uint64_t limit;
	uint64_t used;
	int available_known;
	int limit_known;
} pakr_memory_stats;

static pakr_memory_stats pakr_memory_headroom(void) {
	pakr_memory_stats result = {0};
	vm_statistics64_data_t statistics;
	mach_msg_type_number_t count = HOST_VM_INFO64_COUNT;
	vm_size_t page_size;
	mach_port_t host = mach_host_self();
	if (host_page_size(host, &page_size) == KERN_SUCCESS && host_statistics64(host, HOST_VM_INFO64, (host_info64_t)&statistics, &count) == KERN_SUCCESS) {
		uint64_t pages = (uint64_t)statistics.free_count + statistics.inactive_count;
		int pressure = 0;
		size_t size = sizeof(pressure);
		// Under warning/critical pressure, do not assume inactive pages are reclaimable.
		if (sysctlbyname("kern.memorystatus_vm_pressure_level", &pressure, &size, NULL, 0) == 0 && pressure > 1) {
			pages = statistics.free_count;
		}
		if (page_size != 0 && pages <= UINT64_MAX / page_size) {
			result.available = pages * page_size;
			result.available_known = 1;
		}
	}
	mach_port_deallocate(mach_task_self(), host);

	struct rlimit limit;
	mach_task_basic_info_data_t task;
	count = MACH_TASK_BASIC_INFO_COUNT;
	if (getrlimit(RLIMIT_AS, &limit) == 0 && limit.rlim_cur != RLIM_INFINITY && task_info(mach_task_self(), MACH_TASK_BASIC_INFO, (task_info_t)&task, &count) == KERN_SUCCESS) {
		result.limit = limit.rlim_cur;
		result.used = task.virtual_size;
		result.limit_known = 1;
	}
	return result;
}
*/
import "C"

func detect() headroom {
	var room headroom

	statistics := C.pakr_memory_headroom()
	if statistics.available_known != 0 {
		room.include(uint64(statistics.available))
	}

	if statistics.limit_known != 0 {
		room.limit(uint64(statistics.limit), uint64(statistics.used))
	}

	return room
}
