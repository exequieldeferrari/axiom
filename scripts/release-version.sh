#!/usr/bin/env bash
#
# Validates a release version and prints it back.
#
#   scripts/release-version.sh v0.1.0
#   scripts/release-version.sh --self-test
#
# A release is identified by its tag, and the tag is where the stamped version,
# every archive name, and the release title come from. So the tag has to be
# exactly one shape — vMAJOR.MINOR.PATCH, with nothing before or after it — and
# this is the only place that decides what that means.
#
# The contract here is about public releases only. scripts/build-release.sh does
# not call this and will stamp any version it is handed, which is what lets CI
# build all four platforms under "v0.0.0-ci" without claiming to be a release.
# The gate is the release workflow: it resolves its version through this script
# before building, and only a tag push can reach the publish job.
#
# Prerelease and build suffixes in a version are rejected. Nothing in the pipeline
# needs one: an unpublished build is what workflow_dispatch already produces, so a
# tag like v0.1.0-rc.1 would be a second way to say something Axiom can already
# say — one that reaches archive names and `axiom version` for a release that is
# not allowed to exist. Allowing them is a decision to make when there is a
# release train that needs them.
set -uo pipefail

# Bash matches this against the whole string, so the anchors are exact: no
# component may be empty, and nothing may follow the patch number.
version_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'

valid_version() { [[ $1 =~ $version_pattern ]]; }

# self_test checks the validator against the cases it exists to get right.
self_test() {
	local failures=0 candidate

	for candidate in v0.0.0 v0.1.0 v1.0.0 v12.34.56; do
		if valid_version "$candidate"; then
			printf '  ok      accepts %s\n' "$candidate"
		else
			printf '  FAIL    rejects %s\n' "$candidate"
			failures=$((failures + 1))
		fi
	done

	# The glob this replaced accepted v1x.2.3, v1.2.3foo and v1.2.3-rc.1: in a
	# shell pattern, [0-9]* is one digit followed by anything at all.
	for candidate in \
		main 0.1.0 v v1 v1.2 v1.2.3.4 v1.2.3foo v1x.2.3 v1.2x.3 v1..3 v1.2. \
		v1.2.3-rc.1 v1.2.3+build.1 V1.2.3 " v1.2.3" "v1.2.3 " ""; do
		if valid_version "$candidate"; then
			printf '  FAIL    accepts %s\n' "'$candidate'"
			failures=$((failures + 1))
		else
			printf '  ok      rejects %s\n' "'$candidate'"
		fi
	done

	if [ "$failures" -eq 0 ]; then
		echo "release version validation passed"
		return 0
	fi
	echo "release version validation failed: $failures case(s)"
	return 1
}

if [ $# -ne 1 ]; then
	echo "usage: scripts/release-version.sh <version>|--self-test" >&2
	exit 2
fi

if [ "$1" = "--self-test" ]; then
	self_test
	exit $?
fi

if ! valid_version "$1"; then
	echo "release-version: '$1' is not a release version of the form vMAJOR.MINOR.PATCH" >&2
	exit 1
fi
printf '%s\n' "$1"
