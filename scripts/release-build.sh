#!/usr/bin/env bash
# Builds a release's archives into DIR: for each of linux and darwin, amd64
# and arm64, sesshin_VERSION_OS_ARCH.tar.gz holding both binaries side by side
# (install requires them from one build: implementation-spec.md, Toolchain)
# with LICENSE, README.md, CHANGELOG.md, and the docs/, specs/, and claude/
# (the skill) directories that the README links into, and scripts/opencode.sh;
# then SHA256SUMS over them.
#
#   scripts/release-build.sh v1.0.0 dist
#
# Run from a clean checkout of the tag, so that Go stamps the tag as each
# binary's version and the build info matches between the two.
set -euo pipefail

version=${1:?usage: release-build.sh VERSION DIR}
dir=${2:?usage: release-build.sh VERSION DIR}

if [[ -n $(git status --porcelain) ]]; then
	echo "release-build.sh: uncommitted changes; build from a clean checkout" >&2
	exit 1
fi

mkdir -p "$dir"
# Without cgo on macOS too: golang.org/x/sys/unix calls libc's sysctl
# through Go's own trampolines, and the linker signs darwin/arm64 binaries
# ad hoc, as macOS requires.
for os in linux darwin; do
	for arch in amd64 arm64; do
		name=sesshin_${version#v}_${os}_$arch
		stage=$(mktemp -d)
		mkdir "$stage/$name"
		CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -o "$stage/$name/" ./cmd/sesshin ./cmd/sesshin-hook
		cp -R LICENSE README.md CHANGELOG.md docs specs claude "$stage/$name/"
		mkdir "$stage/$name/scripts"
		cp scripts/opencode.sh "$stage/$name/scripts/"
		tar -C "$stage" -czf "$dir/$name.tar.gz" "$name"
		rm -rf "$stage"
	done
done
(cd "$dir" && sha256sum ./*.tar.gz | sed 's| \./| |' > SHA256SUMS)
