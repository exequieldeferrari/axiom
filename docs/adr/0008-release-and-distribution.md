# 8. Release and distribution

- Status: accepted
- Date: 2026-08-11

## Context

Seven PRs produced a working profiler that only its author could run. Installing
it meant cloning the repository, having a Go toolchain, and knowing that
`make build` puts a binary in `./bin`. The version it printed was `dev` no matter
what. Nothing said which Claude Code versions it had been tried against, there
was no way to undo `axiom init` short of editing JSON by hand, and the only proof
that any of it worked was the maintainer's own terminal history.

That is the gap this PR closes. It is not a change to what Axiom does; it is what
makes what Axiom does available to somebody else, and repeatable.

## Decisions

### v0.1.0, and what it claims

The release exists to make one narrow workflow — observe Claude Code, then
profile what it did — installable, reversible, and demonstrably working end to
end on the platforms it is published for, with the evidence semantics the earlier
ADRs establish.

It claims nothing else. Not a stable CLI, not a schema that will not change, not
completeness of observation, not Windows, not agents other than Claude Code, not
recommendations, comparison, savings, or attribution of consumption to behavior.
The number is `0.1.0` because a 0.x version is the honest way to say that the
interface may move, and the README states the limits in the same words.

It is published as an ordinary GitHub Release, and deliberately not marked as a
GitHub prerelease. The version number already says the interface is unstable;
the prerelease flag says something else — tooling reads it as "not the current
version", and `gh release view --latest` and the releases page both skip it — and
an installable release nobody is pointed at would defeat the purpose of
publishing one. So `gh release create` is called without `--prerelease`, and the
only gate between the run and the public is the draft, which exists for the notes
and not to qualify the release.

### Versions come from tags, never from a constant in source

`internal/version.Version` stays `"dev"` in the tree. A release build stamps the
tag in with `-ldflags -X`, so a tagged release and the version its binary reports
cannot disagree, and nobody has to remember to bump a constant.

A binary installed with `go install ...@v0.1.0` gets no ldflags, and reporting
`dev` for a released binary would be wrong. It reports the module version Go
recorded instead. That fallback deliberately ignores a build made from a working
copy: Go stamps such a build with `vcs.revision` and describes it with a
pseudo-version derived from the commit, which is a true description of the source
and the wrong answer for a version. The presence of the VCS stamp is what tells
the two apart, so a contributor's build says `dev` even when their checkout sits
exactly on a tag.

### Four targets, and not Windows

`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`. Those are the
machines Claude Code is used on.

Two of the four archives are unpacked and run by the acceptance script before a
release can be published: `linux/amd64` on the Ubuntu runner and `darwin/arm64`
on the macOS runner. `darwin/amd64` and `linux/arm64` are cross-compiled and
their checksums are verified on both runners, but they are never executed,
because the runners GitHub gives this workflow cannot execute them. Those two are
published on the strength of the build and of a code base with no
architecture-specific or cgo code, which is weaker evidence than a run, and the
README says so in the same terms rather than describing all four as exercised.

Windows compiles. That is all that is known about it. Its settings locations, its
path handling, and its signal behavior have never been exercised, and publishing
a binary would be a claim that they were. WSL2 users are pointed at the Linux
build, which is a real answer rather than an untested artifact.

### GitHub Actions, not a laptop

A release built by hand is built with whatever Go version, environment, and
uncommitted changes happen to be on that machine, and it is only reproducible by
the person holding it. The workflow builds from a checkout of the tag on a clean
runner, and every artifact of a release is produced by one recorded run that
anybody can read.

### Plain `go build`, not GoReleaser

Four `go build` invocations, `tar`, and `sha256sum` is the whole release. As a
shell script it is about forty lines, it runs locally, and there is no
configuration schema between the reader and what is actually executed.

GoReleaser earns its complexity when a project needs the things it is good at.
Any of these would be a reason to revisit it: publishing to Homebrew or another
package manager, signing or notarization, SBOM or provenance attestations,
Docker images, more than a handful of targets, or generated release notes and
changelogs from commit history. Until then it would be a dependency whose main
effect is to hide a `tar` command.

### `-trimpath` and `CGO_ENABLED=0`, but not `-s -w`

`-trimpath` keeps the builder's directory names out of the binary, which makes
the build independent of where it happened and avoids shipping a maintainer's
home directory in every artifact. `CGO_ENABLED=0` produces a binary with no libc
to match, which is what makes one archive per platform runnable on machines
nobody built it on.

`-s -w` is left off on purpose. It strips the symbol table and DWARF information
to save a few megabytes, and those are exactly what a readable panic trace and a
usable `delve` session need. For a pre-1.0 profiler, a bug report someone can act
on is worth more than a smaller download.

### A candidate build cannot become a release

The workflow answers to a `v*` tag push and to `workflow_dispatch`. The version
comes from the tag on a tag run, and from a required input on a manual run.

Either way it has to be `vMAJOR.MINOR.PATCH` exactly — three non-empty decimal
components, nothing before the `v` and nothing after the patch number — or the
run fails before anything is built. That rule lives in `scripts/release-version.sh`
rather than in the workflow, because a tag filter can only be a glob and a glob
cannot express it: `v[0-9]*.[0-9]*.[0-9]*` reads as "a digit followed by
anything", which accepts `v1x.2.3` and `v1.2.3-rc.1`. The script owns the rule,
prints the version it accepted, and checks itself against the cases it exists to
get right, which CI runs on every change.

Prerelease and build suffixes in a version are rejected. An unpublished build is
what `workflow_dispatch` already produces, so `v0.1.0-rc.1` would be a second way
to say something the pipeline can already say — one that would reach archive names
and `axiom version` for a release that is not allowed to exist. Wanting a release
train is the reason to revisit this; not having one is the reason not to.

### Two version contracts, one gate

`scripts/release-version.sh` owns the *public-release* version contract and
nothing else. `scripts/build-release.sh` is a generic artifact builder: it stamps
whatever version it is handed and validates nothing.

That split is deliberate, and it is not the same as being lax. The gate that
matters is "nothing can be published unless its version passed the guard", and
that is enforced where publishing happens: the release workflow resolves the
version through the validator in a step that fails the whole run when it is
rejected, and the publish job additionally requires a tag push. Validating again
inside the builder would add no protection to that path, because a version can
only reach it through the gate.

What it would cost is the ability to stamp a build that is honestly not a release.
CI compiles all four targets on every change with `v0.0.0-ci`, and that stamp is
the point: an archive from a pull request should not be named or self-report as
`v0.0.0`, which is a version somebody could publish. The same applies to a
maintainer building a debug artifact. Under the stricter rule, both would have to
lie about what they are.

So the invariant is stated per script rather than per version string: the builder
produces artifacts from a stamp, the validator decides which stamps may become a
release, and only the release workflow is required to use both. Both script
headers say this, so the absence of validation in the builder reads as a decision
rather than an omission.

The input is not a convenience. `GITHUB_REF_NAME` on a manual run is the branch,
so taking the version from the ref would stamp `main` into a binary and name the
archives after it. Two guards keep the identity straight: the version is never
derived from a branch, and the publish job runs only for a tag push, so a manual
run leaves its artifacts on the workflow run where they can be downloaded and
tried without a release existing.

The release is created as a draft. The artifacts and checksums come from the
automated run; the notes that say what a version means are written by a person
before it goes out. Publication is the one step that stays a decision.

### The acceptance check runs the artifact, not the source

`scripts/release-check.sh` takes the path to a binary and exercises it: version
output, the empty-log path, `init --dry-run`, a real install and uninstall with
an unrelated hook seeded first, the refusal to install from a temporary
directory, hook payload ingestion, the OTLP receiver, the join that turns a
recorded result size into bytes attributed to a path, both findings, and the exit
status of an unknown command. It runs against a temporary `AXIOM_DATA_DIR`, with
`HOME` and `CLAUDE_CONFIG_DIR` redirected, so it can neither read nor damage the
data and settings of the machine running it.

It does not require Claude Code. Payloads are written by the script and telemetry
comes from the sanitized fixture already in the repository, which is what makes
the result identical on a laptop and on a runner. What that costs is real: it
proves Axiom handles the payloads Axiom expects, not that Claude Code still sends
them. Running the integration against real Claude Code therefore stays a manual
step before a tag, and the README records the version it was last verified
against rather than implying every later one.

The unit tests and this script check different things. The tests check the code;
the script checks the artifact, including the parts no Go test can see — that the
archive contains a runnable binary, that it reports the version it was stamped
with, and that the commands compose.

### Uninstall is part of the release, not a later feature

`axiom init` writes persistent configuration into a file it does not own. A tool
that does that and cannot undo it is asking users to edit JSON to get their
editor back, so `axiom uninstall` ships in the same version as the first
distributed binary.

It removes what an install writes and nothing else. Hooks are recognized by their
argument vector, the same way the installer recognizes them, so a handler left
behind by a binary that has since moved is removed too. The telemetry block is
removed only when all four variables are still an installed Axiom export — three
holding the exact values an install writes, and the endpoint pointing at a
loopback OTLP logs address, which is where an `axiom observe` receiver listens.
Anything else may be somebody's own export pipeline, and silently turning that
off would be worse than leaving four variables behind.

The settings file is never deleted, even when nothing but `{}` remains. Axiom
does not know whether it created the file, an empty document is inert, and the
file may be a symlink into a dotfiles repository where deleting it would do
damage that cannot be undone. The command reports that the document is empty and
leaves the decision to the user. Recorded data is left alone for the same
reason — removing an integration is not a request to throw away the record — and
the command prints where it is.

## Consequences

- A release is one tag push. The failure modes are a version the guard rejects, a
  build that does not compile for one of four targets, or an archive that fails
  the acceptance check, and each of them stops the run before a release exists.
- Pushing a tag the guard rejects — a prerelease suffix, or a typo like `v1.2.3foo` —
  starts a workflow run that fails in its first job. That is louder than a silent
  skip, which is the point: the tag exists in the repository at that moment, and
  the run says so rather than leaving somebody to wonder where their release went.
- CI now runs `go test -race ./...` and builds all four release targets on every
  pull request, so the release build is never the first time a platform is
  compiled. CI is otherwise unchanged: it is not a release workflow, and it does
  not publish anything.
- The build script is shared between CI and the release workflow. That is a
  deliberate exception to keeping the two apart, and it exists because the
  invariant worth protecting is that they build identically.
- Artifacts are unsigned. macOS users who download through a browser will meet
  Gatekeeper's quarantine attribute, which the README addresses by recommending
  `curl` and documenting `xattr -d`. Signing and notarization would require an
  Apple developer account and a secret in CI, and are out of scope for v0.1.0.
- Nothing in the release pipeline knows about Homebrew, Docker, self-update,
  shell completions, or man pages. Each is a distribution decision that should be
  made when there is a user asking for it.
- `axiom uninstall` recognizes Axiom's telemetry variables by what they say rather
  than by a record of what it wrote: three exact values, and a plain-HTTP
  `/v1/logs` endpoint on a loopback address. A user who had independently
  configured Claude Code to export logs to a local collector in exactly that shape
  would have those four variables removed. The alternative — tracking ownership
  inside a file Axiom does not own — is a bigger change to the settings model than
  the remaining risk justifies.

  The loopback requirement is the part that matters, because `/v1/logs` is the
  path every OTLP collector serves. Without it, a team exporting Claude Code's
  logs to their own collector over plain HTTP writes the same four variables Axiom
  does, and an uninstall would silently switch off an export Axiom never set up —
  one an install would have refused to touch, since it requires the endpoint to be
  exactly its own. The port is still not compared, so an install given a
  non-loopback `--addr` is the case this gives up on, and it gives up by leaving
  the variables behind: a leftover somebody can see and delete, rather than a loss
  they have to reconstruct.
- The acceptance script asserts on report wording. That is intentional: the
  wording carries the evidence semantics, and a change to it should be
  deliberate. It does mean a phrasing change updates the script.
