#!/usr/bin/env python3
"""Linux Lzip comparison with matched dictionary/match lengths; CSV to stdout."""

import argparse
import csv
import hashlib
import importlib.util
from pathlib import Path
import random
import statistics
import subprocess
import sys
import tempfile


def benchmark(options, directory):
	root = Path(__file__).resolve().parent.parent
	sys.dont_write_bytecode = True
	spec = importlib.util.spec_from_file_location("codec_bench", root / "scripts" / "bench-bzip3.py")
	common = importlib.util.module_from_spec(spec)
	spec.loader.exec_module(common)
	helper = directory / "measure-memory"
	subprocess.run(["cc", "-O2", str(root / "scripts" / "bench-memory.c"), "-o", str(helper)], check=True)
	size = options.mib * 1048576
	text = b"".join(f"record {index}: independent compression members preserve ordering and validate checksums.\n".encode() for index in range(10000))
	paths = sorted(root.glob("*.go")) + sorted((root / "internal").rglob("*.go"))
	source = b"\n".join(path.read_bytes() for path in paths)
	noise = random.Random(1729).randbytes(size)
	mixed = b"".join(noise[offset:offset + 32768] + common.repeat_to_size(source, 32768) for offset in range(0, size // 2, 32768))
	datasets = {"text": common.repeat_to_size(text, size), "source": common.repeat_to_size(source, size), "mixed": mixed[:size], "random": noise}
	writer = csv.writer(sys.stdout)
	writer.writerow(["dataset", "codec", "level", "threads", "input_bytes", "compressed_bytes", "encode_mib_s", "decode_mib_s", "encode_peak_mib", "decode_peak_mib"])
	cases = [("pakr", options.pakr, 1), ("lzip", options.lzip, 1)]
	if options.plzip:
		cases.extend([("plzip", options.plzip, 1), ("plzip", options.plzip, 4)])
	for dataset, full in datasets.items():
		for length in (4096, size):
			data = full[:length]
			original = directory / "input"
			original.write_bytes(data)
			digest = hashlib.sha256(data).digest()
			for level, dictionary, match in ((1, 1 << 20, 32), (3, 2 << 20, 32), (9, 32 << 20, 273)):
				for codec, executable, threads in cases:
					archive = directory / "compressed"
					decoded = directory / "decoded"
					if codec == "pakr":
						encode = [executable, "pack", str(original), "-f", "lz", "-l", str(level), "-t", str(threads), "-o", "-", "-q"]
						decode = [executable, "unpack", str(archive), "-f", "lz", "-o", str(directory / "extracted"), "-q"]
						plain = directory / "extracted" / "compressed.out"
					else:
						encode = [executable, "-c", "-s", str(min(dictionary, length)), "-m", str(match)]
						decode = [executable, "-d", "-c"]
						if codec == "plzip":
							encode.extend(["-n", str(threads)])
							decode.extend(["-n", str(threads)])
						encode.append(str(original))
						decode.append(str(archive))
						plain = decoded
					timings = []
					for _ in range(options.repeat):
						encode_time, encode_memory = common.measure(encode, archive, helper)
						decode_time, decode_memory = common.measure(decode, decoded, helper)
						if hashlib.sha256(plain.read_bytes()).digest() != digest:
							raise RuntimeError(f"round trip failed: {dataset} {codec}")
						plain.unlink()
						timings.append((length / 1048576 / encode_time, length / 1048576 / decode_time, encode_memory, decode_memory))
					medians = [round(statistics.median(values), 2) for values in zip(*timings)]
					writer.writerow([dataset, codec, level, threads, length, archive.stat().st_size, *medians])
					sys.stdout.flush()


def main():
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("--pakr", required=True)
	parser.add_argument("--lzip", default="lzip")
	parser.add_argument("--plzip", help="optional plzip executable for 1/4-worker member comparisons")
	parser.add_argument("--mib", type=int, default=64)
	parser.add_argument("--repeat", type=int, default=3)
	options = parser.parse_args()
	if options.mib < 1 or options.repeat < 1:
		parser.error("--mib and --repeat must be positive")
	with tempfile.TemporaryDirectory(prefix="pakr-lzip-bench-") as temporary:
		benchmark(options, Path(temporary))


if __name__ == "__main__":
	main()
