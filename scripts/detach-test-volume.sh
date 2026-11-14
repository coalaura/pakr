#!/usr/bin/env bash
set -euo pipefail

image="$RUNNER_TEMP/pakr-tests.sparseimage"
mount="$RUNNER_TEMP/pakr-tests"

# Neither the teardown shell nor its diagnostic tools may retain the test mount.
export TMPDIR="$RUNNER_TEMP"
cd "$RUNNER_TEMP"

# Preparation can fail before creating or attaching the image.
if [[ ! -f "$image" ]]; then
	exit 0
fi

# Resolve only this job's image and mount, never a volume selected by name alone.
device=$(hdiutil info -plist | python3 -c '
import plistlib
import re
import sys

image, mount = sys.argv[1:]
for entry in plistlib.loads(sys.stdin.buffer.read()).get("images", []):
	if entry.get("image-path") == image:
		entities = entry.get("system-entities", [])
		mounts = [entity["mount-point"] for entity in entities if "mount-point" in entity]
		if mounts and mounts != [mount]:
			sys.exit("Test image has unexpected mount points: " + repr(mounts))
		for entity in entities:
			if re.fullmatch(r"/dev/disk[0-9]+", entity.get("dev-entry", "")):
				print(entity["dev-entry"])
				sys.exit(0)
		sys.exit("Cannot identify the disposable test image device")
' "$image" "$mount")

if [[ -z "$device" ]]; then
	printf 'Test image is not attached: %s\n' "$image"
	exit 0
fi

if [[ ! "$device" =~ ^/dev/disk[0-9]+$ ]]; then
	printf 'Unexpected test image device: %s\n' "$device" >&2
	exit 1
fi

# Short retries allow transient filesystem/indexer activity to release the mount.
for attempt in 1 2 3; do
	if hdiutil detach "$device"; then
		exit 0
	fi

	printf 'Detach attempt %s failed for %s (%s)\n' "$attempt" "$image" "$device" >&2
	if [[ "$attempt" -lt 3 ]]; then
		sleep 2
	fi
done

printf 'Open files and working directories on the disposable test volume:\n' >&2
if sudo lsof -nP +f -- "$mount"; then
	:
else
	status=$?
	# lsof returns 1 when no matching open files remain.
	if [[ "$status" -ne 1 ]]; then
		printf 'lsof diagnostics failed with status %s\n' "$status" >&2
	fi
fi

# Tests and artifact upload have finished; only this verified disposable image
# may be force-detached. A failure here must still fail the workflow step.
hdiutil detach -force "$device"
