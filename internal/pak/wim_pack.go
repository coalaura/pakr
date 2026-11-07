package pak

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/coalaura/pakr/internal/membudget"
	"github.com/coalaura/pakr/internal/native"
)

func packWIM(output string, inputs []string, entries []entry, options Options, copying *copier) error {
	session, err := native.NewWIM(copying.ctx)
	if err != nil {
		return err
	}

	defer session.Close()

	codec, level, chunk := wimCompression(options)
	memory := membudget.Resolve(options.Memory)
	overhead := uint64(32 << 20)

	for index := range entries {
		overhead += 4096 + uint64(len(entries[index].path)+len(entries[index].header.Name))*8
	}

	threads, estimate, err := wimWorkers(codec, level, chunk, options.Threads, overhead, memory)
	if err != nil {
		return err
	}

	err = session.Create(codec, level)
	if err != nil {
		return err
	}

	sources := make([]native.WIMSource, len(inputs))
	allowed := make(map[string]struct{}, len(entries)+len(inputs))
	manifest := make(map[string]native.Header, len(entries))
	names := make(map[string]struct{}, len(entries))

	for index := range entries {
		item := &entries[index]

		_, err = wimPath("/"+item.header.Name, filepath.Base(item.header.Name))
		if err != nil {
			return err
		}

		key := strings.ToUpper(item.header.Name)
		if _, exists := names[key]; exists {
			return fmt.Errorf("case-colliding WIM input path: %s", item.header.Name)
		}

		names[key] = struct{}{}
		allowed[item.path] = struct{}{}
		manifest[item.header.Name] = item.header
	}

	for index, input := range inputs {
		absolute, pathErr := filepath.Abs(input)
		if pathErr != nil {
			return pathErr
		}

		target := filepath.Base(absolute)

		if filepath.Clean(input) == "." {
			target = "/"
		}

		sources[index] = native.WIMSource{Path: absolute, Target: target}
		allowed[absolute] = struct{}{}
	}

	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}

	session.Scan = func(name string) (bool, error) {
		name = filepath.Clean(strings.TrimPrefix(name, `\\?\`))
		if name == output {
			return true, nil
		}

		if _, exists := allowed[name]; !exists {
			return false, fmt.Errorf("input tree changed during WIM capture: %s", name)
		}

		return false, nil
	}

	err = session.Capture(sources)
	if err != nil {
		return err
	}

	seen := 0

	err = session.Tree(1, memory/16, func(item native.WIMEntry) error {
		header, headerErr := wimHeader(item)
		if headerErr != nil {
			return headerErr
		}

		if header.Name == "." {
			return nil
		}

		expected, exists := manifest[header.Name]
		if !exists || header.Kind != expected.Kind || header.Kind == native.Regular && header.Size != expected.Size {
			return fmt.Errorf("input tree changed during WIM capture: %s", header.Name)
		}

		seen++

		return nil
	})

	if err != nil {
		return err
	}

	if seen != len(entries) {
		return fmt.Errorf("WIM capture omitted entries: expected %d, captured %d", len(entries), seen)
	}

	var (
		completed uint64
		actual    int
	)

	session.Progress = func(count uint64, workers int) {
		if count > completed {
			copying.progress.Add(int(count - completed))
			completed = count
		}

		if workers != actual {
			actual = workers

			if options.Log != nil {
				_ = options.Log.Infof("WIM: %d native workers, level %d, %d KiB chunks, %d MiB allocation estimate\n", workers, level, chunk>>10, (estimate+(1<<20)-1)>>20)
			}
		}
	}

	return session.Write(output, options.Format.Name == "esd", threads)
}

func wimCompression(options Options) (int, int, int) {
	level := max(1, min(9, options.Level))

	if options.Format.Name == "esd" {
		return native.WIMLZMS, level * 20, 64 << 20
	}

	if level <= 2 {
		return native.WIMXpress, level * 25, 32 << 10
	}

	return native.WIMLZX, 25 + (level-3)*25, 32 << 10
}

func wimWorkers(codec, level, chunk, requested int, overhead, memory uint64) (int, uint64, error) {
	compressor := native.WIMMemory(codec, chunk, level)
	if compressor == 0 {
		return 0, 0, fmt.Errorf("wimlib cannot estimate compressor memory")
	}

	for threads := max(1, min(256, requested)); threads >= 1; threads-- {
		// Match wimlib's maximum parallel queue, including input/output chunks.
		chunks := uint64(1)

		if chunk < 8<<20 {
			chunks = uint64(min(16, 2+threads*(65536/chunk)/16) * 2)
		}

		estimate := overhead + uint64(chunk)*2 + uint64(threads)*(compressor+chunks*uint64(chunk))
		if estimate <= memory {
			return threads, estimate, nil
		}

		if threads == 1 {
			return 0, 0, fmt.Errorf("WIM level %d needs at least %d MiB for one worker; --memory provides %d MiB", level, (estimate+(1<<20)-1)>>20, memory>>20)
		}
	}

	panic("unreachable WIM worker selection")
}
