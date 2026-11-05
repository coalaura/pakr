package membudget

import (
	"testing"
	"testing/fstest"
)

const (
	unifiedMount = "20 1 0:1 / /cg rw - cgroup2 cgroup rw\n"
	legacyMount  = "20 1 0:1 / /cg rw - cgroup cgroup rw,memory\n"
)

type linuxMemoryCase struct {
	name     string
	host     string
	groups   string
	mounts   string
	files    map[string]string
	expected uint64
	known    bool
}

func TestLinuxHeadroom(t *testing.T) {
	cases := []linuxMemoryCase{
		{name: "host", host: "1048576 kB", expected: 1 << 30, known: true},
		{name: "no-information"},
		{name: "malformed-host", host: "nonsense kB"},
		{name: "wrong-unit", host: "1048576 MB"},
		{name: "host-overflow", host: "18446744073709551615 kB"},
		{name: "zero-host", host: "0 kB", known: true},
		{name: "v2", host: "67108864 kB", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "536870912", "cg/app/memory.current": "134217728"}, expected: 384 << 20, known: true},
		{name: "v2-parent", groups: "0::/team/app", mounts: unifiedMount, files: map[string]string{"cg/team/app/memory.max": "max", "cg/team/app/memory.current": "1", "cg/team/memory.max": "1073741824", "cg/team/memory.current": "939524096"}, expected: 128 << 20, known: true},
		{name: "v2-parent-tightest", groups: "0::/team/app", mounts: unifiedMount, files: map[string]string{"cg/team/app/memory.max": "1073741824", "cg/team/app/memory.current": "0", "cg/team/memory.max": "2147483648", "cg/team/memory.current": "2013265920"}, expected: 128 << 20, known: true},
		{name: "v2-unlimited", host: "524288 kB", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "max", "cg/app/memory.current": "0"}, expected: 512 << 20, known: true},
		{name: "v2-missing-usage", host: "524288 kB", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "1"}, expected: 512 << 20, known: true},
		{name: "v2-malformed", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "18446744073709551616", "cg/app/memory.current": "0"}},
		{name: "v2-malformed-usage", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "1000", "cg/app/memory.current": "-1"}},
		{name: "v2-extreme", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "18446744073709551615", "cg/app/memory.current": "1"}, expected: ^uint64(0) - 1, known: true},
		{name: "v2-over-limit", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "1024", "cg/app/memory.current": "2048"}, known: true},
		{name: "v2-tiny", groups: "0::/app", mounts: unifiedMount, files: map[string]string{"cg/app/memory.max": "1024", "cg/app/memory.current": "1023"}, expected: 1, known: true},
		{name: "mount-subtree", groups: "0::/host/team/app", mounts: "20 1 0:1 /host/team /cg rw - cgroup2 cgroup rw", files: map[string]string{"cg/app/memory.max": "1024", "cg/app/memory.current": "256"}, expected: 768, known: true},
		{name: "namespace-root", groups: "0::/", mounts: "20 1 0:1 /docker/id /cg rw - cgroup2 cgroup rw", files: map[string]string{"cg/memory.max": "1024", "cg/memory.current": "256"}, expected: 768, known: true},
		{name: "namespace-child", groups: "0::/child", mounts: "20 1 0:1 /docker/id /cg rw - cgroup2 cgroup rw", files: map[string]string{"cg/child/memory.max": "1024", "cg/child/memory.current": "256"}, expected: 768, known: true},
		{name: "escaped-mount", groups: "0::/app", mounts: `20 1 0:1 / /my\040cgroup rw - cgroup2 cgroup rw`, files: map[string]string{"my cgroup/app/memory.max": "1024", "my cgroup/app/memory.current": "256"}, expected: 768, known: true},
		{name: "unrelated-mount", groups: "0::/app", mounts: "20 1 0:1 /other /cg rw - cgroup2 cgroup rw", files: map[string]string{"cg/memory.max": "1024", "cg/memory.current": "256"}},
		{name: "malformed-path", groups: "0::/../app", mounts: unifiedMount, files: map[string]string{"cg/memory.max": "1", "cg/memory.current": "0"}},
		{name: "malformed-mount", host: "512 kB", groups: "0::/", mounts: "20 1 0:1 / /cg", expected: 512 << 10, known: true},
		{name: "v1", groups: "3:cpu:/other\n5:memory,devices:/app", mounts: legacyMount, files: map[string]string{"cg/app/memory.limit_in_bytes": "536870912", "cg/app/memory.usage_in_bytes": "134217728"}, expected: 384 << 20, known: true},
		{name: "v1-unlimited", host: "1048576 kB", groups: "5:memory:/app", mounts: legacyMount, files: map[string]string{"cg/app/memory.limit_in_bytes": "9223372036854771712", "cg/app/memory.usage_in_bytes": "0"}, expected: 1 << 30, known: true},
		{name: "v1-parent", groups: "5:memory:/team/app", mounts: legacyMount, files: map[string]string{"cg/team/app/memory.limit_in_bytes": "9223372036854771712", "cg/team/memory.use_hierarchy": "1", "cg/team/memory.limit_in_bytes": "1024", "cg/team/memory.usage_in_bytes": "256"}, expected: 768, known: true},
		{name: "v1-no-hierarchy", groups: "5:memory:/team/app", mounts: legacyMount, files: map[string]string{"cg/team/app/memory.limit_in_bytes": "9223372036854771712", "cg/team/memory.use_hierarchy": "0", "cg/team/memory.limit_in_bytes": "1", "cg/team/memory.usage_in_bytes": "0"}},
		{name: "hybrid", groups: "0::/app\n5:memory:/app", mounts: unifiedMount + "21 1 0:2 / /legacy rw - cgroup cgroup rw,memory", files: map[string]string{"cg/app/memory.max": "1024", "cg/app/memory.current": "0", "legacy/app/memory.limit_in_bytes": "512", "legacy/app/memory.usage_in_bytes": "256"}, expected: 256, known: true},
	}

	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			files := make(fstest.MapFS, len(scenario.files)+3)

			files["proc/meminfo"] = &fstest.MapFile{Data: []byte("SwapFree: 999999999 kB\nMemAvailable: " + scenario.host + "\n")}
			files["proc/self/cgroup"] = &fstest.MapFile{Data: []byte(scenario.groups)}
			files["proc/self/mountinfo"] = &fstest.MapFile{Data: []byte(scenario.mounts)}

			for name, content := range scenario.files {
				files[name] = &fstest.MapFile{Data: []byte(content)}
			}

			room := linuxHeadroom(files)
			if room.known != scenario.known || room.bytes != scenario.expected {
				t.Fatalf("got %+v, want known=%t bytes=%d", room, scenario.known, scenario.expected)
			}
		})
	}
}
