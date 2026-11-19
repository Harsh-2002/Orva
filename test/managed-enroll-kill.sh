#!/bin/bash
# Offline disposable-VM run of the enrollment process-kill suite
# (TestManagedEnrollProcessKill in backend/cmd/orva). Every case kills a real
# apply at one artifact-production boundary and then asks the real server
# process whether the interrupted layout is serveable, so most of it needs a
# delegated cgroup, an installed nsjail and a Node runtime that can build.
#
# Usage: test/managed-enroll-kill.sh OUT SRC
#   OUT  existing directory under which a fresh evidence directory is created
#   SRC  worktree to build from; must hold backend/internal/server/ui_dist
#        (run `make embed` there first on a fresh clone)
#
# Environment:
#   ORVA_ENROLL_ROOTFS_TAR  Node runtime rootfs tarball, required. code_sealed
#                           runs a real build inside it, so it must be a working
#                           tree with the current adapters.
#   ORVA_ENROLL_NSJAIL      nsjail executable to copy into the guest, required
#                           (defaults to `nsjail` on PATH).
#   ORVA_ENROLL_NSJAIL_LIBS optional directory of shared libraries for a
#                           dynamically linked nsjail; copied to /scratch/libs
#                           and exported as the guest's LD_LIBRARY_PATH.
#   ORVA_ENROLL_VM_IMAGE    default ubuntu:26.04; must already be pulled,
#                           because the VM runs with --pull never and --no-net.
#   ORVA_ENROLL_BUDGET      default /sys/fs/cgroup/orva-enroll-kill; the guest
#                           cgroup root each case creates its own budget under.
#
# This script destroys what it creates and nothing else: it runs as root inside
# a fresh microsandbox VM, mounts cgroup2 there, and removes the VM on exit.
# Never point it at an existing instance or at a machine you care about.

set -euo pipefail
umask 077

if [ "$#" -ne 2 ]; then
	echo "usage: $0 OUT SRC" >&2
	exit 2
fi
out=$1
src=$2
[ -d "$out" ] || { echo "OUT must be an existing directory: $out" >&2; exit 2; }
[ -f "$src/go.mod" ] || { echo "SRC must be an Orva worktree (no go.mod at $src)" >&2; exit 2; }
[ -d "$src/backend/internal/server/ui_dist" ] ||
	{ echo "SRC has no embedded dashboard; run 'make embed' in $src first" >&2; exit 2; }

rootfs=${ORVA_ENROLL_ROOTFS_TAR:-}
[ -n "$rootfs" ] || { echo "set ORVA_ENROLL_ROOTFS_TAR to a Node runtime rootfs tarball" >&2; exit 2; }
[ -f "$rootfs" ] || { echo "ORVA_ENROLL_ROOTFS_TAR is not a file: $rootfs" >&2; exit 2; }

nsjail=${ORVA_ENROLL_NSJAIL:-}
if [ -z "$nsjail" ]; then
	nsjail=$(command -v nsjail || true)
fi
if [ -z "$nsjail" ] || [ ! -x "$nsjail" ]; then
	echo "set ORVA_ENROLL_NSJAIL to an executable nsjail (none found on PATH)" >&2
	exit 2
fi

libs=${ORVA_ENROLL_NSJAIL_LIBS:-}
[ -z "$libs" ] || [ -d "$libs" ] ||
	{ echo "ORVA_ENROLL_NSJAIL_LIBS is not a directory: $libs" >&2; exit 2; }

image=${ORVA_ENROLL_VM_IMAGE:-ubuntu:26.04}
budget=${ORVA_ENROLL_BUDGET:-/sys/fs/cgroup/orva-enroll-kill}
out=$(mktemp -d "$(cd "$out" && pwd -P)/run.XXXXXX")
printf 'EVIDENCE=%s\n' "$out"
vm="orva-enroll-kill-$$"
owned=0

cleanup() {
	local rc=$?
	trap - EXIT
	if ((owned)); then
		# The guest mounts cgroup2 and unpacks a rootfs, so plain removal can
		# refuse; force only after that, and only for the VM this script made.
		if ! msb remove "$vm" >"$out/remove.log" 2>&1; then
			echo "plain removal of $vm refused; forcing (VM created by this script)" >&2
			msb remove --force "$vm" >>"$out/remove.log" 2>&1 || rc=1
		fi
	fi
	msb list --label task="$vm" --format json >"$out/absence.json" 2>&1 || rc=1
	if [ "$(tr -d '[:space:]' <"$out/absence.json")" != "[]" ]; then
		echo "VM $vm is still present after cleanup:" >&2
		cat "$out/absence.json" >&2
		rc=1
	fi
	exit "$rc"
}
trap cleanup EXIT

echo "== build =="
(
	cd "$src"
	export GOPROXY=off GOWORK=off CGO_ENABLED=0
	make adapters-embed >"$out/adapters-embed.log" 2>&1
	go test -c -o "$out/orva-cmd.test" ./backend/cmd/orva
)
tar -C "$src/backend/runtimes" -cf "$out/adapters.tar" \
	node/adapter.js node/orva.js node/orva.d.ts node/package.json
(
	cd "$out"
	sha256sum orva-cmd.test >bin.sha256
)

echo "== vm $vm ($image) =="
msb create --name "$vm" --label task="$vm" --pull never --no-net \
	-c 2 -m 4G --root-disk 10G --max-duration 15m "$image" >"$out/create.log" 2>&1
owned=1
msb exec "$vm" -- mkdir -p /scratch/libs /scratch/rootfs/node
msb copy "$out/orva-cmd.test" "$vm":/scratch/orva-cmd.test
msb copy "$out/bin.sha256" "$vm":/scratch/bin.sha256
msb copy "$out/adapters.tar" "$vm":/scratch/adapters.tar
msb copy "$rootfs" "$vm":/scratch/node.tar
msb copy "$nsjail" "$vm":/usr/local/bin/nsjail
if [ -n "$libs" ]; then
	shopt -s nullglob
	shared=("$libs"/*)
	shopt -u nullglob
	[ "${#shared[@]}" -gt 0 ] || { echo "no shared libraries in $libs" >&2; exit 2; }
	for f in "${shared[@]}"; do
		msb copy "$f" "$vm":/scratch/libs/"$(basename "$f")"
	done
fi

echo "== run =="
msb exec "$vm" -- /bin/bash -euc '
	cd /scratch
	sha256sum -c bin.sha256
	chmod 0700 orva-cmd.test
	chmod 0755 /usr/local/bin/nsjail
	tar xf node.tar -C rootfs/node --exclude="./dev/*"
	mkdir -p a rootfs/node/opt/orva/node_modules/orva
	tar xf adapters.tar -C a
	cp a/node/adapter.js rootfs/node/opt/orva/
	cp a/node/orva.js a/node/orva.d.ts a/node/package.json \
		rootfs/node/opt/orva/node_modules/orva/
	mountpoint -q /sys/fs/cgroup || mount -t cgroup2 none /sys/fs/cgroup
	echo "+cpu +memory +pids" > /sys/fs/cgroup/cgroup.subtree_control
	mkdir -p "$1"
	rc=0
	env -i PATH=/usr/bin:/bin HOME=/scratch LD_LIBRARY_PATH=/scratch/libs \
		ORVA_OWNER_KERNEL_TEST=disposable-vm ORVA_MANAGED_BOOT_BUDGET="$1" \
		ORVA_ENROLL_VM_ROOTFS=/scratch/rootfs/node \
		./orva-cmd.test -test.v -test.timeout=12m \
		-test.run "^TestManagedEnrollProcessKill$" >kill.log 2>&1 || rc=$?
	printf "%s\n" "$rc" >kill.rc
' bash "$budget" >"$out/vm.log" 2>&1

msb copy "$vm":/scratch/kill.log "$out/kill.log"
msb copy "$vm":/scratch/kill.rc "$out/kill.rc"
read -r test_rc <"$out/kill.rc"
echo "TEST-RC=$test_rc"
awk '/^[[:space:]]*--- (PASS|FAIL|SKIP):/ || /^(PASS|FAIL)$/' "$out/kill.log"
if [[ "$test_rc" != 0 ]] || ! awk '
	BEGIN {
		n = split("intent owner_provisioned runtime_sealed database_committed code_sealed complete", names)
		for (i = 1; i <= n; i++) expected["TestManagedEnrollProcessKill/" names[i]] = 1
	}
	/^[[:space:]]*--- (FAIL|SKIP):/ || /^FAIL$/ { invalid = 1 }
	$1 == "---" && $2 == "PASS:" {
		if ($3 == "TestManagedEnrollProcessKill") parent++
		else if (!($3 in expected) || ++seen[$3] != 1) invalid = 1
	}
	/^PASS$/ { passed++ }
	END {
		for (name in expected) if (seen[name] != 1) invalid = 1
		exit (invalid || parent != 1 || passed != 1)
	}
' "$out/kill.log"; then
	echo "Enrollment gate requires all six named cases to pass without skips; see $out/kill.log" >&2
	exit 1
fi
