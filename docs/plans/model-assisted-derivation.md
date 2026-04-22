# model-assisted artifact derivation

## goal

Replace (or augment) the heuristic artifact-derivation rules with an LLM summariser so that publishing "what I'm working on" produces better `summary` / `status_delta` / `blocker` / `commitment` artifacts than the current branch-name and commit-subject parsing can.

## why

Current derivation (local git tracker, edge-agent derivation) is pattern-matching: branch names, commit subjects, Jira issue status transitions. It is good enough to ship but loses structure — a commit like "refactor: extract JWT validation" becomes "extract jwt validation" with no understanding that it's a refactor of the auth layer. An LLM summariser would produce coherent, teammate-readable status.

## why-not-just-use-it

- cost and latency per publish
- the model can hallucinate specifics (file names that don't exist, PRs that aren't real)
- the model sees raw source data (branch names, commit messages, issue text). If we ship the summariser to the coordination server that violates the "keep raw source data out of central storage" invariant
- needs to run on the user's machine, not server-side

## constraints

- summariser runs locally (same process as the tracker or a separate sidecar the tracker calls)
- the output must be verifiable by the user — every summary cites the evidence it was built from in `source_refs`
- the raw source text never crosses the process boundary to the coordination server
- heuristic path remains the default and the fallback; LLM path is opt-in via env var (`ALICE_TRACK_SUMMARISER=claude` etc.)
- treat the model output as untrusted: it goes into `Content`, not into any field used for routing or permission checks

## shape

- new interface in `internal/tracker/` (or a new `internal/derivation/` package):
  ```go
  type Summariser interface {
      Summarise(ctx context.Context, inputs DerivationInputs) (core.Artifact, error)
  }
  ```
- `DerivationInputs` bundles branch/commits/files/recent-issues — whatever the source gave the tracker
- default implementation: current heuristic code, lifted behind the interface
- new implementation: `ClaudeSummariser` that calls the Claude API with a tight system prompt and a JSON-schema-bound response
- tracker loop asks the configured summariser; on error falls back to the heuristic and logs once per repo per hour
- env vars: `ALICE_TRACK_SUMMARISER` (values: `heuristic` (default), `claude`), `ALICE_TRACK_SUMMARISER_API_KEY`, `ALICE_TRACK_SUMMARISER_MODEL` (default `claude-haiku-4-5-20251001` for latency/cost)

## acceptance criteria

- existing tracker tests still pass with the heuristic summariser (behaviour unchanged)
- new table-driven test for `ClaudeSummariser` against a mocked HTTP transport, covering: valid response, malformed JSON (fall back), API error (fall back), rate-limit (retry once then fall back)
- field-level fuzz: summariser output passes the same artifact-validation code path as any other `POST /v1/artifacts` payload; it cannot inject scripts, control characters, or oversized fields
- published artifact's `SourceRefs` list contains the commits / branch / issue IDs that fed into the summary, so a user reading it can verify

## out of scope

- training / fine-tuning
- on-device models
- summarising teammates' artifacts (the coordination server never sees raw source; it only has already-summarised artifacts)
