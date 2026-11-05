// Package membudget estimates codec allocation budgets from current memory headroom.
package membudget

const (
	automaticMaximum  = 8 << 30
	automaticFallback = 256 << 20
)

type headroom struct {
	bytes uint64
	known bool
}

func (room *headroom) include(available uint64) {
	if !room.known || available < room.bytes {
		room.bytes = available
		room.known = true
	}
}

func (room *headroom) limit(limit, used uint64) {
	room.include(limit - min(limit, used))
}

// Resolve leaves explicit budgets unchanged. Zero requests a fresh automatic estimate.
func Resolve(explicit uint64) uint64 {
	return resolve(explicit, detect)
}

func resolve(explicit uint64, probe func() headroom) uint64 {
	if explicit != 0 {
		return explicit
	}

	room := probe()
	if !room.known {
		return automaticFallback
	}

	return min(room.bytes/4, automaticMaximum)
}
