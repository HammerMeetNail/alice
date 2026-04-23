# plans

One file per remaining work item. Each plan is self-contained: future sessions can read just the relevant plan plus the files it names, without loading the entire history of the project.

Ground rules that apply to every plan:

- keep raw source data out of central storage
- permission checks are deny-by-default
- do not let untrusted content control sinks
- every new list endpoint paginates; every new body read uses `http.MaxBytesReader`
- every storage method accepts `context.Context`
- cross-org isolation is validated on every grant, query, and request path
- new migrations go through `schema_migrations`
- structured logging via `log/slog`
- agents must reach `active` status before they can query, grant, request, or publish

Read `docs/technical-spec.md` for architecture, `docs/threat-model.md` for trust boundaries, and `docs/roadmap.md` for the quick checkbox view. Completed work lives in git history; this directory is for work that still needs doing.

## index

No open plans. The roadmap is caught up — completed work lives in git history. Add a new plan here when a future work item crosses the "needs its own session" bar.

## workflow for future sessions

1. Pick a plan. Read only that plan and the files it names.
2. Confirm acceptance criteria with the user before starting. Plans are a starting sketch, not a binding contract.
3. Work the plan end-to-end: code, tests, docs, commit.
4. When done: delete the plan file (or move it under a `done/` subdirectory if the historical record is worth keeping), tick the roadmap item, update `README.md` if a new env var or CLI flag landed, commit.

Do **not** grow plan files into status-report scrolls. If a plan turns out to be wrong or too big, rewrite it — don't append.
