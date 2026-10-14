package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"

	"github.com/coalaura/pakr/internal/pak"
	"github.com/coalaura/plain/minimal"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := run(ctx, command(), os.Args)
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, application *cli.Command, arguments []string) error {
	err := application.Run(ctx, arguments)
	if err == nil || quietRequested(arguments) {
		return err
	}

	log := minimal.New(minimal.WithTarget(application.ErrWriter), minimal.WithErrorTarget(application.ErrWriter))

	if errors.Is(err, context.Canceled) {
		_ = log.Warnln("cancelled")
	} else {
		_ = log.Errorln(strings.ReplaceAll(err.Error(), "\n", "; "))
	}

	return err
}

func command() *cli.Command {
	return &cli.Command{
		Name:            "pakr",
		Usage:           "fast, uncomplicated packing and unpacking",
		Version:         version,
		HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "quiet", Aliases: []string{"q"}, Usage: "hide status and errors"},
		},
		OnUsageError: usageError,
		Commands: []*cli.Command{
			{
				Name:         "pack",
				Usage:        "pack files or directories into an archive",
				ArgsUsage:    "<paths...>",
				Flags:        flags(true),
				Action:       packAction,
				OnUsageError: usageError,
			},
			{
				Name:         "unpack",
				Usage:        "unpack an archive into a directory",
				ArgsUsage:    "<archive | ->",
				Flags:        flags(false),
				Action:       unpackAction,
				OnUsageError: usageError,
			},
		},
	}
}

func flags(packing bool) []cli.Flag {
	outputUsage := "destination directory (default: .)"
	formatUsage := "input format (default: detect from extension or header)"

	if packing {
		outputUsage = "output file, or - for stdout (default: archive.<format>)"
		formatUsage = "format, e.g. tar.zst, tgz, zip, gz, xz (default: infer output, then tar.zst)"
	}

	result := []cli.Flag{
		&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: outputUsage},
		&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: formatUsage},
		&cli.StringFlag{Name: "threads", Aliases: []string{"t"}, Value: "auto", Usage: "worker count: auto or a positive integer"},
	}

	if packing {
		result = append(result, &cli.IntFlag{Name: "level", Aliases: []string{"l"}, Value: 3, Usage: "compression effort from 1 (fastest) to 9 (smallest)"})
		result = append(result, &cli.StringFlag{Name: "preset", Aliases: []string{"p"}, Usage: "fast, balanced, or best (alternative to --level)"})
	}

	return result
}

func packAction(ctx context.Context, command *cli.Command) error {
	options, err := optionsFor(command, true)
	if err != nil {
		return err
	}

	return pak.Pack(ctx, command.Args().Slice(), options, command.Writer)
}

func unpackAction(ctx context.Context, command *cli.Command) error {
	if command.NArg() != 1 {
		return fmt.Errorf("provide exactly one archive to unpack")
	}

	options, err := optionsFor(command, false)
	if err != nil {
		return err
	}

	return pak.Unpack(ctx, command.Args().First(), options, command.Reader)
}

func optionsFor(command *cli.Command, packing bool) (pak.Options, error) {
	options := pak.Options{Output: command.String("output"), Level: 3, Threads: runtime.GOMAXPROCS(0)}

	threads := command.String("threads")
	if threads != "auto" {
		count, err := strconv.Atoi(threads)
		if err != nil || count < 1 || count > 256 {
			return options, fmt.Errorf("--threads must be auto or an integer from 1 to 256")
		}

		options.Threads = count
	}

	options.Threads = min(options.Threads, 256)

	name := command.String("format")

	var err error

	if name != "" {
		options.Format, err = pak.ParseFormat(name)
	} else if packing {
		if options.Output != "" && options.Output != "-" {
			options.Format, err = pak.InferFormat(options.Output)
		} else {
			options.Format, err = pak.ParseFormat("tar.zst")
		}
	}

	if err != nil {
		return options, err
	}

	if packing {
		options.Level = command.Int("level")

		preset := command.String("preset")
		if preset != "" && command.IsSet("level") {
			return options, fmt.Errorf("choose --level or --preset, not both")
		}

		switch preset {
		case "", "balanced":
		case "fast":
			options.Level = 1
		case "best":
			options.Level = 9
		default:
			return options, fmt.Errorf("unknown preset %q; use fast, balanced, or best", preset)
		}

		if options.Level < 1 || options.Level > 9 {
			return options, fmt.Errorf("--level must be from 1 to 9")
		}
	}

	if !command.Bool("quiet") {
		options.Log = minimal.New(minimal.WithTarget(command.ErrWriter), minimal.WithErrorTarget(command.ErrWriter))

		if terminal(command.ErrWriter) {
			options.Progress = command.ErrWriter
		}
	}

	return options, nil
}

func usageError(_ context.Context, _ *cli.Command, err error, _ bool) error {
	return err
}

func quietRequested(arguments []string) bool {
	quiet := false

	// Parsing may stop at an invalid flag before reaching --quiet.
	for index := 1; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}

		if !strings.HasPrefix(argument, "-") {
			continue
		}

		name, value, assigned := strings.Cut(strings.TrimLeft(argument, "-"), "=")

		switch name {
		case "q", "quiet":
			if !assigned {
				quiet = true

				continue
			}

			parsed, err := strconv.ParseBool(value)
			if err == nil {
				quiet = parsed
			}
		case "o", "output", "f", "format", "t", "threads", "l", "level", "p", "preset":
			if !assigned {
				index++
			}
		}
	}

	return quiet
}

func terminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)

	return ok && term.IsTerminal(int(file.Fd()))
}
