#!/usr/bin/env python3
"""Linux end-to-end codec comparison; CSV to stdout, temporary data auto-removed."""

import argparse
import csv
import hashlib
from pathlib import Path
import random
import statistics
import subprocess
import sys
import tempfile
import time


def measure(arguments, output, helper):
	metrics = helper.parent / "peak-rss"
	with output.open("wb") as destination, tempfile.TemporaryFile() as errors:
		start = time.perf_counter()
		process = subprocess.run([str(helper), str(metrics), *arguments], stdout=destination, stderr=errors)
		elapsed = time.perf_counter() - start
		if process.returncode:
			errors.seek(0)
			raise RuntimeError(f"{arguments}: {errors.read().decode(errors='replace')}")
	return elapsed, int(metrics.read_text()) / 1024


def repeat_to_size(seed, size):
	return (seed * ((size + len(seed) - 1) // len(seed)))[:size]


def cases(pakr, official):
	for block, level in ((1, 1), (16, 3)):
		for threads in (1, 4):
			yield "pakr-bz3", level, block, threads, pakr
			yield "official-bz3", level, block, threads, official
	for codec in ("bz2", "zst", "xz"):
		for threads in (1, 4):
			yield f"pakr-{codec}", 3, 0, threads, pakr


def benchmark(options, directory):
	root = Path(__file__).resolve().parent.parent
	helper = directory / "measure-memory"
	subprocess.run(["cc", "-O2", str(root / "scripts" / "bench-memory.c"), "-o", str(helper)], check=True)
	size = options.mib * 1024 * 1024
	randomizer = random.Random(1729)
	text = b"".join(f"record {index}: independent compression blocks preserve original ordering and validate checksums.\n".encode() for index in range(10000))
	paths = sorted(root.glob("*.go")) + sorted((root / "internal").rglob("*.go"))
	source = b"\n".join(path.read_bytes() for path in paths)
	noise = randomizer.randbytes(size)
	mixed = b"".join(noise[offset:offset + 32768] + repeat_to_size(source, 32768) for offset in range(0, size // 2, 32768))
	datasets = {"text": repeat_to_size(text, size), "source": repeat_to_size(source, size), "mixed": mixed[:size], "random": noise}
	writer = csv.writer(sys.stdout)
	writer.writerow(["dataset", "codec", "level", "block_mib", "threads", "input_bytes", "compressed_bytes", "encode_mib_s", "decode_mib_s", "encode_peak_mib", "decode_peak_mib"])

	for dataset, data in datasets.items():
		original = directory / "input"
		original.write_bytes(data)
		digest = hashlib.sha256(data).digest()
		for codec, level, block, threads, executable in cases(options.pakr, options.bzip3):
			archive = directory / "compressed"
			decoded = directory / "decoded"
			timings = []
			for _ in range(options.repeat):
				if codec == "official-bz3":
					encode = [executable, "-c", "-b", str(block), "-j", str(threads), str(original)]
					decode = [executable, "-d", "-c", "-j", str(threads), str(archive)]
					plain = decoded
				else:
					format_name = codec.removeprefix("pakr-")
					encode = [executable, "pack", str(original), "-f", format_name, "-l", str(level), "-t", str(threads), "-o", "-", "-q"]
					decode = [executable, "unpack", str(archive), "-f", format_name, "-t", str(threads), "-o", str(directory / "extracted"), "-q"]
					plain = directory / "extracted" / "compressed.out"
				encode_time, encode_memory = measure(encode, archive, helper)
				decode_time, decode_memory = measure(decode, decoded, helper)
				if hashlib.sha256(plain.read_bytes()).digest() != digest:
					raise RuntimeError(f"round trip failed: {dataset} {codec}")
				plain.unlink()
				timings.append((len(data) / 1048576 / encode_time, len(data) / 1048576 / decode_time, encode_memory, decode_memory))
			medians = [round(statistics.median(values), 2) for values in zip(*timings)]
			writer.writerow([dataset, codec, level, block, threads, len(data), archive.stat().st_size, *medians])
			sys.stdout.flush()


def main():
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("--pakr", required=True, help="absolute path to the built pakr binary")
	parser.add_argument("--bzip3", default="bzip3", help="official BZIP3 CLI path")
	parser.add_argument("--mib", type=int, default=64, help="size of each synthetic corpus (default: 64 MiB)")
	parser.add_argument("--repeat", type=int, default=3, help="median of this many runs (default: 3)")
	options = parser.parse_args()
	if options.mib < 1 or options.repeat < 1:
		parser.error("--mib and --repeat must be positive")
	with tempfile.TemporaryDirectory(prefix="pakr-bench-") as temporary:
		benchmark(options, Path(temporary))


if __name__ == "__main__":
	main()
