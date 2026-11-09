package pak

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
)

const (
	DefaultMaxOutput   = 16 << 40
	DefaultMaxFileSize = 1 << 40
	DefaultMaxEntries  = 10000000
)

// ExtractionLimits bounds payload bytes and archive entries. Zero disables a limit.
type ExtractionLimits struct {
	MaxOutput   uint64
	MaxFileSize uint64
	MaxEntries  uint64
}

type extractionBudget struct {
	ctx     context.Context
	limits  ExtractionLimits
	cancel  context.CancelCauseFunc
	mutex   sync.Mutex
	output  uint64
	entries uint64
	err     error
}

type extractionWriter struct {
	target   io.Writer
	budget   *extractionBudget
	declared int64
	written  uint64
}

var ErrExtractionLimit = errors.New("extraction resource limit exceeded")

func (budget *extractionBudget) reject(resource string, limit uint64) error {
	if budget.err == nil {
		budget.err = fmt.Errorf("%w: %s (%d)", ErrExtractionLimit, resource, limit)
		budget.cancel(budget.err)
	}

	return budget.err
}

func (budget *extractionBudget) entry(size int64) error {
	budget.mutex.Lock()
	defer budget.mutex.Unlock()

	if budget.err != nil {
		return budget.err
	}

	limit := budget.limits.MaxEntries
	if budget.entries >= math.MaxInt64 || limit != 0 && budget.entries >= limit {
		return budget.reject("archive entries", limit)
	}

	budget.entries++

	if size >= 0 {
		limit = budget.limits.MaxFileSize
		if limit != 0 && uint64(size) > limit {
			return budget.reject("individual file bytes", limit)
		}

		return budget.reserve(uint64(size))
	}

	return nil
}

// The caller holds mutex. Known sizes are reserved before decoding; unknown sizes
// reserve each write, so parallel workers cannot independently spend the same budget.
func (budget *extractionBudget) reserve(size uint64) error {
	limit := budget.limits.MaxOutput
	if budget.output > math.MaxInt64 || size > math.MaxInt64-budget.output || limit != 0 && size > limit-budget.output {
		return budget.reject("total output bytes", limit)
	}

	budget.output += size

	return nil
}

func (writer *extractionWriter) Write(data []byte) (int, error) {
	budget := writer.budget

	budget.mutex.Lock()

	err := budget.err
	size := uint64(len(data))
	limit := budget.limits.MaxFileSize

	if err == nil && (writer.written > math.MaxInt64 || size > math.MaxInt64-writer.written || limit != 0 && size > limit-writer.written) {
		err = budget.reject("individual file bytes", limit)
	}

	if err == nil && writer.declared >= 0 && size > uint64(writer.declared)-writer.written {
		err = errors.New("entry exceeds declared size")
	}

	if err == nil && writer.declared < 0 {
		err = budget.reserve(size)
	}

	budget.mutex.Unlock()

	if err != nil {
		return 0, err
	}

	count, err := writer.target.Write(data)

	writer.written += uint64(count)

	return count, err
}

func newExtractionBudget(ctx context.Context, limits *ExtractionLimits, cancel context.CancelCauseFunc) *extractionBudget {
	settings := ExtractionLimits{MaxOutput: DefaultMaxOutput, MaxFileSize: DefaultMaxFileSize, MaxEntries: DefaultMaxEntries}

	if limits != nil {
		settings = *limits
	}

	return &extractionBudget{ctx: ctx, limits: settings, cancel: cancel}
}

func stdinSpoolLimit(limits *ExtractionLimits) int64 {
	maximum := uint64(DefaultMaxOutput)

	if limits != nil && limits.MaxOutput != 0 {
		maximum = min(maximum, limits.MaxOutput)
	}

	// Allow container metadata and incompressible framing, but always bound stdin.
	return int64(maximum + maximum/8 + 256<<20)
}
