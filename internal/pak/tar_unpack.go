package pak

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/coalaura/pakr/internal/native"
)

const (
	tarWorkerSize       = 256 << 10
	tarWorkerEntries    = 16
	tarWorkerCopyBuffer = 64 << 10
)

// A slot is owned by the producer until start, then by its worker until wait.
// Batches amortize dispatch and keep sibling files together without unbounded read-ahead.
type tarWorker struct {
	jobs    chan struct{}
	done    chan error
	data    []byte
	entries []native.Header
	used    int
	active  bool
}

type tarWorkers struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	pending sync.WaitGroup
	workers []tarWorker
	next    int
}

func (worker *tarWorker) run(extract *extractor, cancel context.CancelCauseFunc) {
	defer extract.close()

	var source bytes.Reader

	for range worker.jobs {
		var (
			offset int64
			err    error
		)

		for _, header := range worker.entries {
			source.Reset(worker.data[offset : offset+header.Size])
			offset += header.Size

			err = extract.entry(header, &source)
			if err != nil {
				cancel(err)

				break
			}
		}

		worker.done <- err
	}
}

func (worker *tarWorker) start() {
	if !worker.active && len(worker.entries) != 0 {
		worker.jobs <- struct{}{}
		worker.active = true
	}
}

func (worker *tarWorker) wait() error {
	if len(worker.entries) == 0 {
		return nil
	}

	worker.start()

	err := <-worker.done

	worker.active = false
	worker.entries = worker.entries[:0]
	worker.used = 0

	return err
}

func (workers *tarWorkers) waitPath(name string) error {
	for index := range workers.workers {
		worker := &workers.workers[index]

		for _, header := range worker.entries {
			if tarPathsConflict(header.Name, name) {
				err := worker.wait()
				if err != nil {
					return err
				}

				break
			}
		}
	}

	return context.Cause(workers.ctx)
}

func (workers *tarWorkers) submit(header native.Header, source io.Reader) error {
	err := workers.waitPath(header.Name)
	if err != nil {
		return err
	}

	worker := &workers.workers[workers.next]

	if worker.active {
		err = worker.wait()
		if err != nil {
			return err
		}
	}

	if int(header.Size) > tarWorkerSize-worker.used || len(worker.entries) == tarWorkerEntries {
		worker.start()
		workers.next = (workers.next + 1) % len(workers.workers)

		worker = &workers.workers[workers.next]

		err = worker.wait()
		if err != nil {
			return err
		}
	}

	err = context.Cause(workers.ctx)
	if err != nil {
		return err
	}

	if worker.data == nil {
		worker.data = make([]byte, tarWorkerSize)
		worker.entries = make([]native.Header, 0, tarWorkerEntries)
	}

	end := worker.used + int(header.Size)

	_, err = io.ReadFull(source, worker.data[worker.used:end])
	if err != nil {
		return fmt.Errorf("read %q: %w", header.Name, err)
	}

	worker.entries = append(worker.entries, header)
	worker.used = end

	return nil
}

func (workers *tarWorkers) drain() error {
	for index := range workers.workers {
		workers.workers[index].start()
	}

	for index := range workers.workers {
		err := workers.workers[index].wait()
		if err != nil {
			return err
		}
	}

	return context.Cause(workers.ctx)
}

func (workers *tarWorkers) close(prior error) error {
	if prior != nil {
		workers.cancel(prior)
	}

	for index := range workers.workers {
		if prior == nil {
			workers.workers[index].start()
		}

		close(workers.workers[index].jobs)
	}

	workers.pending.Wait()
	err := context.Cause(workers.ctx)
	workers.cancel(nil)

	if errors.Is(prior, err) {
		return prior
	}

	return errors.Join(prior, err)
}

func unpackTar(source io.Reader, threads int, extract *extractor) (result error) {
	archive := tar.NewReader(source)

	var workers *tarWorkers

	defer func() {
		if workers != nil {
			result = workers.close(result)
		}
	}()

	for {
		err := extract.copying.ctx.Err()
		if err != nil {
			return err
		}

		if workers != nil {
			err = context.Cause(workers.ctx)
			if err != nil {
				return err
			}
		}

		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		item := native.Header{Name: header.Name, Size: header.Size, Mode: uint32(header.Mode), Modified: header.ModTime, Link: header.Linkname}

		switch header.Typeflag {
		case tar.TypeReg:
			item.Kind = native.Regular
		case tar.TypeDir:
			item.Kind = native.Directory
		case tar.TypeSymlink:
			item.Kind = native.Symlink
		case tar.TypeLink:
			item.Kind = native.Hardlink
		default:
			return fmt.Errorf("unsupported tar entry type: %s", header.Name)
		}

		if threads > 1 && item.Kind == native.Regular && item.Size <= tarWorkerSize {
			item.Name, err = cleanName(item.Name)
			if err != nil {
				return err
			}

			if item.Name == "." || item.Size < 0 {
				return fmt.Errorf("invalid entry: %q", header.Name)
			}

			// Reserve before buffering so queued payloads cannot bypass extraction limits.
			err = extract.budget.entry(item.Size)
			if err != nil {
				return err
			}

			if workers == nil {
				workers = newTarWorkers(threads, extract)
			}

			err = workers.submit(item, archive)
		} else {
			// Directory metadata is deferred until finish; unrelated directories
			// can be created while files run. Links and streaming files drain first.
			if workers != nil {
				if item.Kind == native.Directory {
					item.Name, err = cleanName(item.Name)
					if err == nil {
						err = workers.waitPath(item.Name)
					}
				} else {
					err = workers.drain()
				}

				if err != nil {
					return err
				}
			}

			err = extract.entry(item, archive)
		}

		if err != nil {
			return err
		}
	}
}

func newTarWorkers(threads int, extract *extractor) *tarWorkers {
	ctx, cancel := context.WithCancelCause(extract.copying.ctx)
	// Match the ZIP worker cap: at most 5 MiB of payload/copy buffers and
	// 32 cached directory handles per worker, independent of archive size.
	workers := &tarWorkers{ctx: ctx, cancel: cancel, workers: make([]tarWorker, min(threads, 16))}
	directories := extract.directoryState()

	for index := range workers.workers {
		worker := &workers.workers[index]
		worker.jobs = make(chan struct{}, 1)
		worker.done = make(chan error, 1)

		workers.pending.Go(func() {
			copying := copier{ctx: ctx, progress: extract.copying.progress, buffer: make([]byte, tarWorkerCopyBuffer)}
			local := extractor{root: extract.root, copying: &copying, directories: directories, budget: extract.budget, prechecked: true}

			worker.run(&local, cancel)
		})
	}

	return workers
}

func tarPathsConflict(first, second string) bool {
	for {
		firstPart, firstRest, firstMore := strings.Cut(first, "/")
		secondPart, secondRest, secondMore := strings.Cut(second, "/")

		// Conservatively serialize case and trailing-dot/space aliases as well
		// as equal paths and file/ancestor conflicts on all platforms.
		if !strings.EqualFold(strings.TrimRight(firstPart, ". "), strings.TrimRight(secondPart, ". ")) {
			return false
		}

		if !firstMore || !secondMore {
			return true
		}

		first = firstRest
		second = secondRest
	}
}
