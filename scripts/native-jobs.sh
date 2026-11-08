#!/usr/bin/env bash

# Leave 1 GiB for the linker, Go and the OS, then budget 2 GiB per compiler.
# JOBS is an upper bound, not a way to bypass CPU/memory limits.
native_jobs() {
	local cpus memory available key value unit limit usage used quota period
	cpus=$(getconf _NPROCESSORS_ONLN)
	if command -v nproc >/dev/null; then cpus=$(nproc); fi
	memory=0
	if [[ -r /proc/meminfo ]]; then
		while read -r key value unit; do
			if [[ "$key" == MemAvailable: ]]; then memory=$((value * 1024)); break; fi
		done < /proc/meminfo
	elif command -v sysctl >/dev/null; then
		memory=$(sysctl -n hw.memsize 2>/dev/null || printf 0)
	fi

	# Account for both cgroup versions used by Docker runners.
	for limit in /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory/memory.limit_in_bytes; do
		if [[ -r "$limit" ]]; then
			read -r available < "$limit"
			if [[ "$available" =~ ^[0-9]+$ ]]; then
				usage=${limit%/*}/memory.current
				if [[ "$limit" == */memory.limit_in_bytes ]]; then usage=${limit%/*}/memory.usage_in_bytes; fi
				if [[ -r "$usage" ]]; then
					read -r used < "$usage"
					available=$((available - used))
				fi
				if (( available < memory )); then memory=$available; fi
			fi
		fi
	done
	if [[ -r /sys/fs/cgroup/cpu.max ]]; then
		read -r quota period < /sys/fs/cgroup/cpu.max
		if [[ "$quota" != max ]]; then
			limit=$(((quota + period - 1) / period))
			if (( limit < cpus )); then cpus=$limit; fi
		fi
	elif [[ -r /sys/fs/cgroup/cpu/cpu.cfs_quota_us ]]; then
		read -r quota < /sys/fs/cgroup/cpu/cpu.cfs_quota_us
		read -r period < /sys/fs/cgroup/cpu/cpu.cfs_period_us
		if (( quota > 0 )); then
			limit=$(((quota + period - 1) / period))
			if (( limit < cpus )); then cpus=$limit; fi
		fi
	fi

	native_job_limit "$cpus" "$memory" "${JOBS:-4}"
}

native_job_limit() {
	local cpus=$1 memory=$2 requested=$3 workers
	if [[ ! "$requested" =~ ^[1-9][0-9]*$ ]]; then
		printf 'JOBS must be a positive integer\n' >&2
		return 1
	fi
	workers=$(((memory - 1073741824) / 2147483648))
	if (( workers < 1 )); then workers=1; fi
	if (( workers > cpus )); then workers=$cpus; fi
	if (( workers > requested )); then workers=$requested; fi
	printf '%s\n' "$workers"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then native_jobs; fi
