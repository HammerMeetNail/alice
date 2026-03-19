# Repository Guidelines

## Project Structure & Module Organization
The repository is currently design-first. The root contains `README.md`, which defines the product direction and planned runtime layout, and `docs/`, which holds the active specifications:

- `docs/technical-spec.md`: system architecture, scope, and MVP boundaries
- `docs/threat-model.md`: security goals, trust boundaries, and threat analysis

The `README.md` also sketches future implementation directories such as `cmd/`, `internal/`, `api/`, `deploy/`, `scripts/`, and `test/`. Until those exist, keep design work in `docs/` and update the root `README.md` when the planned structure changes.

## Build, Test, and Development Commands
There is no build pipeline or automated test suite checked in yet. Current work is Markdown authoring and review.

Run these examples from the repository root:

- `git diff -- README.md docs/`: inspect documentation changes before committing
- `rg -n "TODO|FIXME" README.md docs/`: find unfinished notes
- `sed -n '1,120p' docs/technical-spec.md`: review a section with line-stable output

If you introduce tooling later, document the exact command here and in `README.md`.

## Coding Style & Naming Conventions
Use Markdown with clear ATX headings (`#`, `##`, `###`) and short, direct paragraphs. Match the existing style:

- sentence-case section headings
- concise bullet lists for requirements and scope
- lowercase, hyphen-separated filenames such as `technical-spec.md`

Prefer explicit security and architecture terminology over shorthand. When changing scope, update both the spec and threat model if the decision affects trust boundaries or data handling.

## Testing Guidelines
Review changes manually for consistency, broken cross-references, and contradictions between documents. Treat the pair of docs as a single source of truth: security assumptions in `threat-model.md` should align with flows described in `technical-spec.md`.

## Commit & Pull Request Guidelines
Existing history uses short, imperative commit subjects such as `Initial commit` and `Add readme, tech-spec, threat model`. Follow that pattern: one-line, imperative, capitalized, no trailing period.

PRs should include a brief summary, the reason for the change, and the affected documents. Link the relevant issue when one exists. Include screenshots only when a PR adds rendered diagrams or other visual assets.
