#!/usr/bin/env bash
#
# Checks that a packaged axiom binary works end to end.
#
#   scripts/release-check.sh <path-to-axiom> [expected-version]
#
# This is the acceptance gate for a release artifact: it runs the binary that
# will be published, not one it builds itself, because the thing worth checking
# is the archive somebody downloads.
#
# Everything happens in a temporary directory with AXIOM_DATA_DIR, HOME and
# CLAUDE_CONFIG_DIR redirected into it, so a check can neither read nor damage
# the machine's own Axiom data or Claude Code settings.
#
# Claude Code is deliberately not required. Hook payloads are written here and
# telemetry comes from the sanitized fixture in the repository, which keeps the
# result the same on every machine. Running Axiom against real Claude Code stays
# a manual step before a tag.

set -uo pipefail

die() {
	echo "release-check: $1" >&2
	exit 2
}

if [ $# -lt 1 ]; then
	die "usage: scripts/release-check.sh <path-to-axiom> [expected-version]"
fi
[ -x "$1" ] || die "not an executable file: $1"
axiom="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
expected_version="${2:-}"

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$repo/internal/otlp/testdata/claude_logs.json"
[ -f "$fixture" ] || die "missing telemetry fixture: $fixture"
command -v curl >/dev/null 2>&1 || die "curl is required"

work="$(mktemp -d)"
receiver_pid=""
cleanup() {
	if [ -n "$receiver_pid" ] && kill -0 "$receiver_pid" 2>/dev/null; then
		kill "$receiver_pid" 2>/dev/null
		wait "$receiver_pid" 2>/dev/null
	fi
	rm -rf "$work"
}
trap cleanup EXIT

export AXIOM_DATA_DIR="$work/data"
export HOME="$work/home"
export CLAUDE_CONFIG_DIR="$work/home/.claude"

out="$work/out"
project="$work/project"
mkdir -p "$HOME" "$out" "$project"

failures=0

ok() { printf '  ok    %s\n' "$1"; }

bad() {
	printf '  FAIL  %s\n' "$1"
	failures=$((failures + 1))
}

# have checks that a captured output contains every given string.
have() {
	local description="$1" file="$2" missing=()
	shift 2
	for needle in "$@"; do
		grep -qF -- "$needle" "$file" || missing+=("$needle")
	done
	if [ ${#missing[@]} -eq 0 ]; then
		ok "$description"
	else
		bad "$description (missing: ${missing[*]})"
	fi
}

matches() {
	if grep -Eq -- "$3" "$2"; then
		ok "$1"
	else
		bad "$1 (no line matching: $3)"
	fi
}

echo "Checking $axiom"
echo

echo "Version"
if ! "$axiom" version >"$out/version.txt" 2>&1; then
	bad "'axiom version' exited non-zero"
fi
reported="$(cat "$out/version.txt")"
if [ -n "$expected_version" ]; then
	if [ "$reported" = "axiom $expected_version" ]; then
		ok "reports the version it was built with ($reported)"
	else
		bad "reports '$reported', want 'axiom $expected_version'"
	fi
else
	matches "reports a version ($reported)" "$out/version.txt" '^axiom .+$'
fi

echo
echo "Empty state"
if "$axiom" profile >"$out/empty-profile.txt" 2>&1; then
	have "an empty log explains itself instead of failing" "$out/empty-profile.txt" \
		"No events recorded yet." "axiom init"
else
	bad "'axiom profile' failed with no events recorded"
fi

echo
echo "Installation"
if (cd "$project" && "$axiom" init --dry-run --telemetry) >"$out/init-dry-run.txt" 2>&1; then
	have "a dry run says what it would write" "$out/init-dry-run.txt" \
		"Would write" "$project/.claude/settings.local.json" \
		"hook claude" "SessionStart" "PostToolUse" "PostToolUseFailure" "SessionEnd" \
		"CLAUDE_CODE_ENABLE_TELEMETRY"
else
	bad "'axiom init --dry-run' failed: $(cat "$out/init-dry-run.txt")"
fi
settings="$project/.claude/settings.local.json"
if [ -e "$settings" ]; then
	bad "the dry run wrote $settings"
else
	ok "a dry run writes nothing"
fi

# An unrelated hook is seeded first, because the property that matters about
# uninstall is what it leaves alone.
mkdir -p "$(dirname "$settings")"
cat >"$settings" <<'JSON'
{
  "permissions": {"allow": ["Bash(git *)"]},
  "hooks": {
    "PostToolUse": [
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "/usr/local/bin/fmt.sh"}]}
    ]
  }
}
JSON

if (cd "$project" && "$axiom" init --telemetry) >"$out/init.txt" 2>&1; then
	have "installs hooks and telemetry" "$out/init.txt" "Installed Axiom hooks in" "$settings" \
		"start a new session"
	have "the settings name this binary and the hook it runs" "$settings" "$axiom" '"hook"' '"claude"'
	have "telemetry is configured for the local receiver" "$settings" \
		"CLAUDE_CODE_ENABLE_TELEMETRY" "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
else
	bad "'axiom init' failed: $(cat "$out/init.txt")"
fi

echo
echo "Removal"
if (cd "$project" && "$axiom" uninstall) >"$out/uninstall.txt" 2>&1; then
	have "uninstall reports what it took out" "$out/uninstall.txt" \
		"Removed Axiom from" "$settings" "4 hooks" "telemetry configuration"
	if grep -qF -- "$axiom" "$settings"; then
		bad "an Axiom hook survived the uninstall"
	else
		ok "no Axiom configuration is left behind"
	fi
	have "the user's own settings and hooks survive" "$settings" \
		"fmt.sh" '"matcher": "Edit"' "permissions"
else
	bad "'axiom uninstall' failed: $(cat "$out/uninstall.txt")"
fi
if (cd "$project" && "$axiom" uninstall) >"$out/uninstall-again.txt" 2>&1; then
	have "uninstalling twice is a success" "$out/uninstall-again.txt" "not installed"
else
	bad "a second 'axiom uninstall' failed: $(cat "$out/uninstall-again.txt")"
fi

# A binary run out of a temporary directory would leave a hook pointing at a
# path that stops existing, so installing from one has to be refused.
cp "$axiom" "$work/axiom-copy"
if (cd "$project" && "$work/axiom-copy" init --dry-run) >"$out/temp-guard.txt" 2>&1; then
	bad "installing from a temporary directory was allowed"
else
	have "refuses to install a hook for a temporary binary" "$out/temp-guard.txt" \
		"temporary directory" "somewhere permanent"
fi

echo
echo "Hook ingestion"
session="11111111-2222-3333-4444-555555555555"
turn="aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
# The invocation the fixture reports a result size for. Using it here is what
# joins the two streams, and the byte total below is the proof that it worked.
measured_call="toolu_000000000000000000000"
handler="$project/internal/api/handler.go"
readme="$project/README.md"

hook_noise=""
hook_failed=0
send() {
	local output
	if ! output="$(printf '%s' "$1" | "$axiom" hook claude 2>&1)"; then
		hook_failed=1
	fi
	hook_noise="$hook_noise$output"
}

send "$(
	cat <<JSON
{"hook_event_name":"SessionStart","session_id":"$session","cwd":"$project",
 "source":"startup","model":"claude-sonnet-5"}
JSON
)"
# The same file read twice in one turn: the repeated-read finding.
send "$(
	cat <<JSON
{"hook_event_name":"PostToolUse","session_id":"$session","prompt_id":"$turn","cwd":"$project",
 "tool_name":"Read","tool_use_id":"toolu_read_handler_1","duration_ms":11,
 "tool_input":{"file_path":"$handler"}}
JSON
)"
send "$(
	cat <<JSON
{"hook_event_name":"PostToolUse","session_id":"$session","prompt_id":"$turn","cwd":"$project",
 "tool_name":"Read","tool_use_id":"toolu_read_handler_2","duration_ms":9,
 "tool_input":{"file_path":"$handler"}}
JSON
)"
# A different file, read once, under the invocation telemetry measured.
send "$(
	cat <<JSON
{"hook_event_name":"PostToolUse","session_id":"$session","prompt_id":"$turn","cwd":"$project",
 "tool_name":"Read","tool_use_id":"$measured_call","duration_ms":2,
 "tool_input":{"file_path":"$readme"}}
JSON
)"
# The same command failing three times: the repeated-failure finding.
for attempt in 1 2 3; do
	send "$(
		cat <<JSON
{"hook_event_name":"PostToolUseFailure","session_id":"$session","prompt_id":"$turn","cwd":"$project",
 "tool_name":"Bash","tool_use_id":"toolu_bash_$attempt","duration_ms":2100,
 "tool_input":{"command":"go test ./internal/api/..."},
 "error":"Exit code 1\n--- FAIL: TestHandler"}
JSON
	)"
done
send "$(
	cat <<JSON
{"hook_event_name":"SessionEnd","session_id":"$session","cwd":"$project","reason":"exit"}
JSON
)"

if [ "$hook_failed" -eq 0 ]; then
	ok "every hook payload was accepted"
else
	bad "a hook payload was rejected"
fi
# A hook that writes to stdout is read by Claude Code as hook output, so silence
# is part of the contract.
if [ -z "$hook_noise" ]; then
	ok "hooks write nothing to stdout"
else
	bad "a hook wrote output Claude Code would read: $hook_noise"
fi

events="$AXIOM_DATA_DIR/events.jsonl"
if [ -f "$events" ]; then
	recorded="$(wc -l <"$events" | tr -d '[:space:]')"
	if [ "$recorded" = "8" ]; then
		ok "recorded all 8 events"
	else
		bad "recorded $recorded events, want 8"
	fi
else
	bad "no event log was written to $events"
fi

echo
echo "Telemetry receiver"
"$axiom" observe --addr 127.0.0.1:0 >"$out/observe.txt" 2>&1 &
receiver_pid=$!
endpoint=""
for _ in $(seq 1 100); do
	endpoint="$(sed -n 's|^Axiom is listening on ||p' "$out/observe.txt" | head -n 1)"
	[ -n "$endpoint" ] && break
	sleep 0.1
done

if [ -z "$endpoint" ]; then
	bad "the receiver did not start: $(cat "$out/observe.txt")"
else
	ok "the receiver is listening on $endpoint"
	code="$(curl -sS -o "$out/export.txt" -w '%{http_code}' \
		-X POST -H 'Content-Type: application/json' \
		--data-binary "@$fixture" "$endpoint" 2>"$out/curl.txt")"
	if [ "$code" = "200" ]; then
		ok "accepted an OTLP export"
	else
		bad "the export returned HTTP ${code:-none}: $(cat "$out/curl.txt" "$out/export.txt")"
	fi

	usage="$AXIOM_DATA_DIR/usage.jsonl"
	written=0
	for _ in $(seq 1 50); do
		if [ -f "$usage" ]; then
			written="$(wc -l <"$usage" | tr -d '[:space:]')"
			[ "$written" = "3" ] && break
		fi
		sleep 0.1
	done
	if [ "$written" = "3" ]; then
		ok "kept the 3 measurements and dropped the rest of the export"
	else
		bad "recorded $written usage records, want 3"
	fi
fi

if [ -n "$receiver_pid" ]; then
	kill "$receiver_pid" 2>/dev/null
	wait "$receiver_pid" 2>/dev/null
	receiver_pid=""
	have "the receiver reports what it recorded on exit" "$out/observe.txt" "Recorded 3 usage records"
fi

echo
echo "Profile"
if "$axiom" profile >"$out/profile.txt" 2>&1; then
	have "reports the observed activity" "$out/profile.txt" \
		"Observed operations" "Work by path"
	matches "counts the 3 file operations" "$out/profile.txt" '^  File +3 '
	matches "counts the 3 shell operations" "$out/profile.txt" '^  Shell +3 '
	have "attributes measured bytes to the path they were read from" "$out/profile.txt" \
		"43 B read" "Read bytes were measured for 1 of 3 reads"
	have "reports the repeated read" "$out/profile.txt" \
		"Repeated file read" "internal/api/handler.go"
	# One of the two reads is reported as potentially redundant: the first one
	# is the work, and only what came after it could have been avoided.
	matches "counts the redundant reads" "$out/profile.txt" 'Potentially redundant reads +1'
	have "reports the repeated failure" "$out/profile.txt" "Repeated failed attempt"
	matches "counts the failed attempts" "$out/profile.txt" 'Failed attempts +3'
	matches "names the exit code the attempts shared" "$out/profile.txt" 'Same exit code +1'
	# The two say different things about the reports and neither grades the
	# finding, so both have to survive packaging.
	matches "says what the attempts reported" "$out/profile.txt" \
		'Failure reporting +detail beyond status, every attempt'
	matches "says whether the reports were the same" "$out/profile.txt" 'Reports +identical'
	have "reports what the model consumed in those turns" "$out/profile.txt" \
		"Observed model consumption" "Model requests"
	have "keeps the findings' language within the evidence" "$out/profile.txt" \
		"2 findings" \
		"with no agent modification observed in between" \
		"in one turn, with only read-only operations in between"
else
	bad "'axiom profile' failed: $(cat "$out/profile.txt")"
fi

echo
echo "Usage errors"
status=0
"$axiom" definitely-not-a-command >"$out/unknown.txt" 2>&1 || status=$?
if [ "$status" -eq 2 ]; then
	ok "an unknown command exits 2"
else
	bad "an unknown command exited $status, want 2"
fi
have "an unknown command explains itself" "$out/unknown.txt" "unknown command" "Usage:"

echo
if [ "$failures" -eq 0 ]; then
	echo "release check passed"
	exit 0
fi
echo "release check failed: $failures problem(s)"
if [ -s "$out/profile.txt" ]; then
	echo
	echo "--- axiom profile ---"
	cat "$out/profile.txt"
fi
exit 1
