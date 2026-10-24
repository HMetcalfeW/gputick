# CLAUDE.md

Read `AGENTS.md` before doing anything in this repository. It is the single source of truth for coding standards, testing expectations, architecture, data rules, and git workflow. Follow it exactly and do not reinvent conventions it already defines.

The three rules that matter most:

1. Stage with `git add` but never commit, push, tag, merge, or open a pull request. The owner reviews the staged diff and commits.
2. `make lint` and `make test` must be clean before you stage anything.
3. Never commit data: no Parquet files, no snapshots, nothing under `data/`, no fixtures larger than about 200KB.

Where to look:

- `docs/PLAN.md` for current priorities and what to work on next.
- `docs/METHODOLOGY.md` before touching anything in `internal/normalize/` or adding a source.
- `docs/SCHEMA.md` before changing any type in `pkg/schema/`.
