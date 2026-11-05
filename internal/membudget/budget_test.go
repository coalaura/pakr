package membudget

import "testing"

type budgetCase struct {
	name      string
	estimates []uint64
	expected  uint64
}

func TestAutomaticBudget(t *testing.T) {
	cases := []budgetCase{
		{name: "small-vps", estimates: []uint64{512 << 20}, expected: 128 << 20},
		{name: "workstation", estimates: []uint64{64 << 30}, expected: 8 << 30},
		{name: "container", estimates: []uint64{64 << 30, 384 << 20}, expected: 96 << 20},
		{name: "low", estimates: []uint64{4096}, expected: 1024},
		{name: "exhausted", estimates: []uint64{8 << 30, 0}, expected: 0},
		{name: "round-down", estimates: []uint64{3}, expected: 0},
		{name: "missing", expected: 256 << 20},
		{name: "maximum-integer", estimates: []uint64{^uint64(0)}, expected: 8 << 30},
	}

	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			actual := resolve(0, func() headroom {
				var room headroom

				for _, estimate := range scenario.estimates {
					room.include(estimate)
				}

				return room
			})

			if actual != scenario.expected {
				t.Fatalf("budget %d, want %d", actual, scenario.expected)
			}
		})
	}
}

func TestExplicitBudgetSkipsProbe(t *testing.T) {
	values := []uint64{1, 256 << 20, 2 << 30, 8 << 30, ^uint64(0)}

	for _, value := range values {
		actual := resolve(value, func() headroom {
			t.Fatal("explicit budget probed memory")

			return headroom{}
		})

		if actual != value {
			t.Fatalf("changed override %d to %d", value, actual)
		}
	}
}

func TestHeadroomSubtraction(t *testing.T) {
	var room headroom

	room.limit(^uint64(0), 1)

	if !room.known || room.bytes != ^uint64(0)-1 {
		t.Fatal(room)
	}

	room.limit(100, 101)

	if room.bytes != 0 {
		t.Fatal("usage above limit must saturate at zero")
	}
}

func BenchmarkAutomaticBudget(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		Resolve(0)
	}
}

func BenchmarkExplicitBudget(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		Resolve(2 << 30)
	}
}
