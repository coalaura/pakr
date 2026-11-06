#!/usr/bin/env python3
"""Linux BSC settings/scaling and codec comparison, verified round trips, CSV output."""

import argparse
import csv
import hashlib
from pathlib import Path
import random
import statistics
import struct
import subprocess
import sys
import tempfile
import time


def measure(arguments, helper, directory):
	metrics = directory / "peak-rss"
	with tempfile.TemporaryFile() as messages:
		start = time.perf_counter()
		process = subprocess.run([str(helper), str(metrics), *map(str, arguments)], stdout=messages, stderr=messages)
		elapsed = time.perf_counter() - start
		if process.returncode:
			messages.seek(0)
			raise RuntimeError(f"{arguments}: {messages.read().decode(errors='replace')}")
	return elapsed, int(metrics.read_text()) / 1024


def repeat(seed, size):
	return (seed * ((size + len(seed) - 1) // len(seed)))[:size]


def settings(level):
	blocks = (1, 4, 8, 16, 25, 32, 48, 64, 64)
	coders = (0, 0, 1, 1, 1, 2, 2, 2, 2)
	hashes = (15, 15, 15, 16, 16, 17, 18, 18, 18)
	flags = [f"-b{blocks[level - 1]}", f"-e{coders[level - 1]}", f"-H{hashes[level - 1]}", "-M128" if level == 9 else "-M72", "-ca" if level >= 8 else "-cf"]
	if level == 9:
		flags.append("-r")
	return flags


def cases(options):
	if options.candidates:
		for block in (1, 8, 25, 64):
			for coder in (0, 1, 2):
				yield "official-bsc", f"b{block}-e{coder}", 1, [f"-b{block}", f"-e{coder}"]
		for sorter in (3, 4, 5, 6):
			yield "official-bsc", f"st{sorter}", 1, ["-b8", f"-m{sorter}", "-e1"]
		for hash_bits, match in ((15, 32), (18, 32), (18, 72), (18, 128), (20, 72)):
			yield "official-bsc", f"H{hash_bits}-M{match}", 1, ["-b8", "-e2", f"-H{hash_bits}", f"-M{match}"]
		for flags in (["-ca"], ["-r"], ["-ca", "-r"], ["-p"]):
			yield "official-bsc", "".join(flags), 1, ["-b8", "-e2", *flags]
		return
	for level in options.levels:
		for workers in options.workers:
			for codec in ("pakr-bsc", "official-bsc"):
				yield codec, level, workers, settings(level)
	for codec in ("bz3", "xz", "zst"):
		for workers in options.workers:
			yield f"pakr-{codec}", 3, workers, []


def benchmark(options, directory):
	root = Path(__file__).resolve().parent.parent
	helper = directory / "measure-memory"
	subprocess.run(["cc", "-O2", str(root / "scripts" / "bench-memory.c"), "-o", str(helper)], check=True)
	size = options.mib << 20
	randomizer = random.Random(1729)
	text = b"".join(f"record {index}: independent compression blocks preserve original ordering and validate checksums.\n".encode() for index in range(10000))
	paths = sorted(root.glob("*.go")) + sorted((root / "internal").rglob("*.go"))
	source = b"\n".join(path.read_bytes() for path in paths)
	noise = randomizer.randbytes(size)
	mixed = b"".join(noise[offset:offset + 32768] + repeat(source, 32768) for offset in range(0, size // 2, 32768))
	records = b"".join(struct.pack("<4I", index, index // 100, index % 256, index * 7) for index in range(65536))
	datasets = {"text": repeat(text, size), "source": repeat(source, size), "mixed": mixed[:size], "random": noise, "records": repeat(records, size)}
	writer = csv.writer(sys.stdout)
	writer.writerow(["dataset", "sha256", "codec", "level_or_candidate", "workers", "input_bytes", "compressed_bytes", "encode_mib_s", "decode_mib_s", "encode_peak_mib", "decode_peak_mib"])
	for name in options.datasets:
		data = datasets[name]
		original = directory / "input"
		original.write_bytes(data)
		digest = hashlib.sha256(data).hexdigest()
		for codec, level, workers, flags in cases(options):
			archive = directory / "compressed"
			decoded = directory / "decoded"
			timings = []
			for _ in range(options.repeat):
				archive.unlink(missing_ok=True)
				if codec == "official-bsc":
					encode = [options.bsc, "e", original, archive, *flags, f"-T{workers}"]
					decode = [options.bsc, "d", archive, decoded, f"-T{workers}"]
					plain = decoded
				else:
					format_name = codec.removeprefix("pakr-")
					encode = [options.pakr, "pack", original, "-f", format_name, "-l", level, "-t", workers, "--memory", "8GiB", "-o", archive, "-q"]
					decode = [options.pakr, "unpack", archive, "-f", format_name, "-t", workers, "--memory", "8GiB", "-o", directory / "extracted", "-q"]
					plain = directory / "extracted" / "compressed.out"
				encode_time, encode_memory = measure(encode, helper, directory)
				decode_time, decode_memory = measure(decode, helper, directory)
				if hashlib.sha256(plain.read_bytes()).hexdigest() != digest:
					raise RuntimeError(f"round trip failed: {name} {codec}")
				plain.unlink()
				timings.append((len(data) / 1048576 / encode_time, len(data) / 1048576 / decode_time, encode_memory, decode_memory))
			medians = [round(statistics.median(values), 2) for values in zip(*timings)]
			writer.writerow([name, digest, codec, level, workers, len(data), archive.stat().st_size, *medians])
			sys.stdout.flush()


def main():
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("--pakr", required=True)
	parser.add_argument("--bsc", required=True, help="pristine official BSC 3.4.0 CLI built with OpenMP")
	parser.add_argument("--mib", type=int, default=128)
	parser.add_argument("--repeat", type=int, default=3)
	parser.add_argument("--workers", type=int, nargs="+", default=[1, 4, 16])
	parser.add_argument("--levels", type=int, nargs="+", default=[1, 3, 9])
	parser.add_argument("--datasets", nargs="+", choices=["text", "source", "mixed", "random", "records"], default=["text", "source", "mixed", "random", "records"])
	parser.add_argument("--candidates", action="store_true")
	options = parser.parse_args()
	if options.mib < 1 or options.repeat < 1 or any(level < 1 or level > 9 for level in options.levels):
		parser.error("positive size/repeat and levels 1-9 required")
	with tempfile.TemporaryDirectory(prefix="pakr-bsc-bench-") as temporary:
		benchmark(options, Path(temporary))


if __name__ == "__main__":
	main()
