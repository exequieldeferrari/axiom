## Summary

<!--
One or two sentences: what this change makes possible, and what it preserves.
A change that touches what Axiom claims about a recording should say so here.
-->

## What changed

<!--
The change as a reader of the diff would describe it, not as the issue framed
it. One bullet per behaviour a user or a caller can observe. Keep what the diff
shows separate from what you intended by it, and say which is which. Link the
ADR if this change decided something: docs/adr.

A generated summary (Copilot's, or any other) is a draft of this section and
not evidence. Check it against the diff before you keep it, and delete anything
it asserts that you have not confirmed.
-->

-

## Evidence and semantics

<!--
Axiom reports what a recording holds and refuses to infer past it, so a change
is not complete until this boundary is stated. Say what the new output claims,
and say what it deliberately does not claim. Omit this section only for changes
that cannot reach analysis or output at all.
-->

-

## Validation

<!--
What you ran, not what CI will run. Check only what you actually ran: an
unchecked box means it was not run, which is a fact a reviewer can use. Delete
what does not apply, and add anything you ran that is not listed.
-->

- [ ] `go test -race ./...`
- [ ] `make lint`
- [ ] `make build`
- [ ] `make test`
- [ ] `./scripts/release-check.sh ./bin/axiom` (artifact-level behaviour)

## Review focus

<!--
Where review is worth spending. Name the decisions you are least sure of and
the boundaries a reviewer should push on, rather than asking for a general look.
-->

-
