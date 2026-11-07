import datetime
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time


def timestamp(message):
	print(f"{datetime.datetime.now(datetime.timezone.utc).isoformat()} {message}", flush=True)


def collect_reports(directory, started):
	for label, source in (("user", Path.home() / "Library/Logs/DiagnosticReports"), ("system", Path("/Library/Logs/DiagnosticReports"))):
		try:
			for report in source.glob("package.test*"):
				if report.is_file() and report.stat().st_mtime >= started:
					shutil.copy2(report, directory / f"{label}-{report.name}")
		except OSError as error:
			timestamp(f"Crash reports unavailable from {source}: {error}")


def main():
	root = Path(__file__).resolve().parent.parent
	directory = root / "dist/macos-parallel-diagnostics"
	directory.mkdir(parents=True, exist_ok=True)
	binary = root / "dist/tests/internal/native/package.test"
	command = [str(binary), "-test.run=^TestParallelReaders$/^1$", "-test.v", "-test.count=1", "-test.timeout=60s"]
	started = time.time()
	timestamp("START TestParallelReaders/1 (45s watchdog, 60s Go timeout)")
	status = 1
	try:
		with (directory / "test.log").open("w") as output:
			binary.chmod(binary.stat().st_mode | 0o111)
			process = subprocess.Popen(command, cwd=root / "internal/native", stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
			try:
				status = process.wait(timeout=45)
			except subprocess.TimeoutExpired:
				status = 124
				timestamp(f"WATCHDOG pid={process.pid}: collecting native stacks")
				with (directory / "sample-command.log").open("w") as diagnostics:
					try:
						subprocess.run(["/usr/bin/sample", str(process.pid), "5", "-file", str(directory / "sample.txt")], stdout=diagnostics, stderr=subprocess.STDOUT, timeout=10, check=False)
					except (OSError, subprocess.TimeoutExpired) as error:
						print(f"Native sampling unavailable: {error}", file=diagnostics)
						timestamp(f"Native sampling unavailable: {error}")
				try:
					os.killpg(process.pid, signal.SIGQUIT)
					process.wait(timeout=5)
				except (ProcessLookupError, subprocess.TimeoutExpired):
					pass
			finally:
				# Kill the process group even if Go's timeout cannot interrupt a native call.
				if process.poll() is None:
					try:
						os.killpg(process.pid, signal.SIGKILL)
					except ProcessLookupError:
						pass
				process.wait(timeout=5)
	finally:
		if status != 0:
			# ReportCrash writes asynchronously; keep this wait bounded.
			for attempt in range(3):
				collect_reports(directory, started)
				time.sleep(2)
		log = directory / "test.log"
		if log.exists():
			print(log.read_text(errors="replace"), end="", flush=True)
		timestamp(f"END TestParallelReaders/1 status={status} elapsed={time.time() - started:.1f}s")
		(directory / "result.txt").write_text(f"start={datetime.datetime.fromtimestamp(started, datetime.timezone.utc).isoformat()}\nend={datetime.datetime.now(datetime.timezone.utc).isoformat()}\nstatus={status}\n")
	return status if status >= 0 else 128 - status


if __name__ == "__main__":
	sys.exit(main())
