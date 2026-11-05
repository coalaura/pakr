package membudget

import (
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
)

// Linux represents an unlimited v1 limit as LONG_MAX rounded down to a page.
const cgroupUnlimited = uint64(1<<63 - 1<<16)

var mountEscapes = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

func detect() headroom {
	return linuxHeadroom(os.DirFS("/"))
}

func linuxHeadroom(files fs.FS) headroom {
	var room headroom

	data, _ := fs.ReadFile(files, "proc/meminfo")

	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "MemAvailable:" || fields[2] != "kB" {
			continue
		}

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil && value <= ^uint64(0)/1024 {
			room.include(value * 1024)
		}
	}

	membership, _ := fs.ReadFile(files, "proc/self/cgroup")
	mounts, _ := fs.ReadFile(files, "proc/self/mountinfo")

	groups := string(membership)

	for line := range strings.SplitSeq(string(mounts), "\n") {
		before, after, found := strings.Cut(line, " - ")
		if !found {
			continue
		}

		if !strings.HasPrefix(after, "cgroup ") && !strings.HasPrefix(after, "cgroup2 ") {
			continue
		}

		mount := strings.Fields(before)
		detail := strings.Fields(after)

		if len(mount) < 6 || len(detail) < 3 {
			continue
		}

		unified := detail[0] == "cgroup2"
		if !unified && (detail[0] != "cgroup" || !commaContains(detail[2], "memory")) {
			continue
		}

		root := unescapeMount(mount[3])
		point := unescapeMount(mount[4])

		if !validAbsolute(root) || !validAbsolute(point) {
			continue
		}

		for entry := range strings.SplitSeq(groups, "\n") {
			_, controllers, found := strings.Cut(entry, ":")
			if !found {
				continue
			}

			controllers, group, found := strings.Cut(controllers, ":")
			if !found || !validAbsolute(group) || unified != (controllers == "") {
				continue
			}

			if !unified && !commaContains(controllers, "memory") {
				continue
			}

			relative := group

			if root != "/" && (group == root || strings.HasPrefix(group, root+"/")) {
				relative = strings.TrimPrefix(group, root)
			}

			// In a cgroup namespace membership is relative to the namespace root,
			// while mountinfo can still expose the mount's host-relative root.
			directory := path.Join(point, relative)

			name := strings.TrimPrefix(directory, "/")
			if name == "" {
				name = "."
			}

			info, err := fs.Stat(files, name)
			if err != nil || !info.IsDir() {
				continue
			}

			cgroupHeadroom(files, directory, point, unified, &room)
		}
	}

	return room
}

func cgroupHeadroom(files fs.FS, directory, mount string, unified bool, room *headroom) {
	limitName := "memory.limit_in_bytes"
	usageName := "memory.usage_in_bytes"

	if unified {
		limitName = "memory.max"
		usageName = "memory.current"
	}

	leaf := true

	for {
		// Legacy v1 ancestors only charge descendants with hierarchy enabled.
		applicable := unified || leaf
		if !applicable {
			hierarchy, hierarchyOK := readNumber(files, path.Join(directory, "memory.use_hierarchy"))
			applicable = hierarchyOK && hierarchy == 1
		}

		if applicable {
			limit, limitOK := readNumber(files, path.Join(directory, limitName))
			used, usedOK := readNumber(files, path.Join(directory, usageName))

			if limitOK && usedOK && (unified || limit < cgroupUnlimited) {
				room.limit(limit, used)
			}
		}

		if directory == mount {
			break
		}

		directory = path.Dir(directory)
		leaf = false
	}
}

func readNumber(files fs.FS, name string) (uint64, bool) {
	data, err := fs.ReadFile(files, strings.TrimPrefix(name, "/"))
	if err != nil {
		return 0, false
	}

	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return value, err == nil
}

func commaContains(list, item string) bool {
	for value := range strings.SplitSeq(list, ",") {
		if value == item {
			return true
		}
	}

	return false
}

func validAbsolute(value string) bool {
	return strings.HasPrefix(value, "/") && path.Clean(value) == value
}

func unescapeMount(value string) string {
	// mountinfo escapes spaces, tabs, newlines and backslashes as octal bytes.
	return mountEscapes.Replace(value)
}
