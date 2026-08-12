#!/usr/bin/env bash
#
# Builds the axiom release artifacts.
#
#   scripts/build-release.sh <version> [output-dir]
#
# The version is stamped into the binary and is what `axiom version` reports.
# CI runs this on every change with a throwaway version, so the build a release
# depends on is the build every pull request already exercised.
#
# This script stamps whatever version it is given and deliberately does not
# validate it. Which versions may become a public release is a separate contract,
# owned by scripts/release-version.sh, and the release workflow resolves the
# version through it before calling this — so a version can only reach a release
# through that gate. Builds that are honestly not releases, such as CI's
# "v0.0.0-ci", depend on this script accepting a stamp the validator would refuse.
# Anything that publishes must validate first; this script only builds.
set -euo pipefail

if [ $# -lt 1 ]; then
	echo "usage: scripts/build-release.sh <version> [output-dir]" >&2
	exit 2
fi

version="$1"
outdir="${2:-dist}"

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

# Archive names carry the bare version, the way most release assets read; the
# stamped version keeps its leading v, the way the tag is written.
archive_version="${version#v}"

platforms=(
	darwin/arm64
	darwin/amd64
	linux/amd64
	linux/arm64
)

mkdir -p "$outdir"
# Only Axiom's own artifacts are cleared, so passing a directory that holds
# something else cannot delete it.
rm -f "$outdir"/axiom_*.tar.gz "$outdir"/checksums.txt

for platform in "${platforms[@]}"; do
	os="${platform%/*}"
	arch="${platform#*/}"
	name="axiom_${archive_version}_${os}_${arch}"
	stage="$outdir/$name"

	rm -rf "$stage"
	mkdir -p "$stage"

	# CGO_ENABLED=0 produces one file that needs no libc to match, which is
	# what makes an archive runnable on a machine nobody built it on.
	# -trimpath keeps the builder's directory names out of the binary.
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
		-trimpath \
		-ldflags "-X github.com/exequieldeferrari/axiom/internal/version.Version=${version}" \
		-o "$stage/axiom" \
		./cmd/axiom

	# The license and the README travel with the binary: someone who downloads
	# one archive should not have to visit the repository to read either.
	cp LICENSE README.md "$stage/"

	tar -czf "$outdir/$name.tar.gz" -C "$stage" axiom LICENSE README.md
	rm -rf "$stage"
	echo "built $name.tar.gz"
done

cd "$outdir"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum axiom_*.tar.gz >checksums.txt
else
	shasum -a 256 axiom_*.tar.gz >checksums.txt
fi
echo "wrote checksums.txt"
