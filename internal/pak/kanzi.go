package pak

import (
	"errors"
	"io"

	"github.com/coalaura/pakr/internal/native"
)

func logKanzi(options Options, settings native.KanziSettings) {
	if options.Log != nil {
		_ = options.Log.Infof("Kanzi: %d jobs, %d KiB blocks, %d MiB allocation budget reserved\n", settings.Jobs, settings.BlockSize>>10, (settings.Memory+(1<<20)-1)>>20)
	}
}

func unpackKanziStdout(source io.Reader, options Options, budget *extractionBudget, progress *Progress) error {
	if options.Stdout == nil {
		return errors.New("kanzi stdout output is unavailable")
	}

	err := budget.entry(-1)
	if err != nil {
		return err
	}

	stream, err := native.NewKanziReader(budget.ctx, source, options.Threads, options.Memory)
	if err != nil {
		return err
	}

	defer stream.Close()

	logKanzi(options, stream.Settings)

	progress.Phase("unpack", 0)

	writer := extractionWriter{target: options.Stdout, budget: budget, declared: -1}
	copying := copier{ctx: budget.ctx, progress: progress, buffer: make([]byte, native.BufferSize)}

	_, err = copying.copy(&writer, stream)
	if err != nil {
		return err
	}

	err = stream.Close()
	if err == nil {
		progress.Complete("unpacked", "stdout", -1)
	}

	return err
}
