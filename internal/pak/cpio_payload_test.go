package pak

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

type cpioPayloadCase struct {
	name    string
	size    int64
	payload string
	want    string
}

func TestCPIOPayloadSizes(t *testing.T) {
	cases := []cpioPayloadCase{
		{name: "exact", size: 3, payload: "abc"},
		{name: "empty", size: 0},
		{name: "unknown", size: -1, payload: "abc"},
		{name: "truncated", size: 4, payload: "abc", want: "size mismatch"},
		{name: "missing", size: 4, want: "size mismatch"},
		{name: "oversized", size: 2, payload: "abc", want: "exceeds declared size"},
		{name: "unexpected", size: 0, payload: "abc", want: "exceeds declared size"},
		{name: "invalid", size: -2, want: "invalid entry size"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			extract := cpioTestExtractor(t, nil)
			header := native.Header{Name: "entry", Size: test.size, Kind: native.Hardlink}

			err := drainCPIOEntry(header, strings.NewReader(test.payload), extract)
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("drain: %v, want %q", err, test.want)
			}
		})
	}
}

func TestCPIODeferredPayloadValidation(t *testing.T) {
	extract := cpioTestExtractor(t, nil)

	var links cpioLinks

	header := native.Header{Name: "first", Size: 0, Mode: 0o644, Links: 2}

	err := links.entry(header, strings.NewReader("unexpected"), extract)
	if err == nil || !strings.Contains(err.Error(), "exceeds declared size") {
		t.Fatalf("deferred payload accepted: %v", err)
	}

	if len(links["first"].pending) != 0 {
		t.Fatal("invalid entry queued for later extraction")
	}

	links = nil
	header.Size = 3

	err = links.entry(header, strings.NewReader("abc"), extract)
	if err != nil {
		t.Fatal(err)
	}

	header.Name = "second"
	header.Kind = native.Hardlink
	header.Link = "first"

	err = links.entry(header, strings.NewReader("ab"), extract)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("truncated duplicate payload accepted: %v", err)
	}

	_, err = extract.root.Stat("second")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid hardlink created: %v", err)
	}
}

func TestCPIOPayloadLimitsAndCancellation(t *testing.T) {
	limits := []ExtractionLimits{{MaxOutput: 2}, {MaxFileSize: 2}}

	for _, limit := range limits {
		extract := cpioTestExtractor(t, &limit)
		header := native.Header{Name: "entry", Size: -1, Kind: native.Hardlink}

		err := drainCPIOEntry(header, strings.NewReader("abc"), extract)
		if !errors.Is(err, ErrExtractionLimit) || !errors.Is(context.Cause(extract.copying.ctx), ErrExtractionLimit) {
			t.Fatalf("drain bypassed limit: %v", err)
		}
	}

	extract := cpioTestExtractor(t, nil)
	extract.budget.cancel(context.Canceled)

	err := drainCPIOEntry(native.Header{Size: 3}, strings.NewReader("abc"), extract)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("drain ignored cancellation: %v", err)
	}
}

func TestCPIOHardlinkBudgets(t *testing.T) {
	payloads := []string{"", "payload"}

	for _, payload := range payloads {
		var archive bytes.Buffer

		appendCPIOTestEntry(&archive, "first", "", 0o100644, 2)
		appendCPIOTestEntry(&archive, "second", payload, 0o100644, 2)
		appendCPIOTestEntry(&archive, "TRAILER!!!", "", 0, 1)

		format, _ := ParseFormat("cpio")
		limits := []ExtractionLimits{{MaxEntries: 1}}

		if payload != "" {
			limits = append(limits, ExtractionLimits{MaxOutput: 1}, ExtractionLimits{MaxFileSize: 1})
		}

		for _, limit := range limits {
			output := t.TempDir()

			err := Unpack(t.Context(), "-", Options{Format: format, Output: output, Limits: &limit}, bytes.NewReader(archive.Bytes()))
			if !errors.Is(err, ErrExtractionLimit) {
				t.Fatalf("deferred entries bypassed limit: %v", err)
			}

			entries, err := os.ReadDir(output)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed deferred entries left files: %v", err)
			}
		}
	}
}

func cpioTestExtractor(t *testing.T, limits *ExtractionLimits) *extractor {
	t.Helper()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		root.Close()
	})

	ctx, cancel := context.WithCancelCause(t.Context())

	t.Cleanup(func() {
		cancel(nil)
	})

	extract := &extractor{
		root:    root,
		copying: &copier{ctx: ctx, buffer: make([]byte, 16)},
		budget:  newExtractionBudget(ctx, limits, cancel),
	}
	t.Cleanup(extract.close)

	return extract
}
