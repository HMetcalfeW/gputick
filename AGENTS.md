# AGENTS.md - Coding Standards & AI Development Guidelines

This document defines the coding standards, testing expectations, and conventions for AI-assisted development on gputick. Any AI agent contributing to this codebase must read and follow these guidelines. When a rule here conflicts with a tool default or a habit carried over from another project, this document wins.

gputick is an open, auditable time series of GPU rental prices and availability, by provider and region, across hyperscalers and neoclouds. It sits on top of the catalogs that schedulers already maintain (SkyPilot catalog, dstack gpuhunt) and adds region-level history retained forever, availability as a first-class series, and honest labeling of what each number is. The project is pre-alpha and no data has been collected yet. Correctness and auditability of the data matter more than feature count.

## Language & Toolchain

- **Go 1.26+** with modules enabled. Module path: `github.com/HMetcalfeW/gputick`.
- **Linter**: `golangci-lint` v2.11, configured in `.golangci.yml` (v2 format). All code must pass with zero issues before staging.
- **Test framework**: `testing` stdlib + `github.com/stretchr/testify` (`assert`/`require`).
- **CLI framework**: Cobra. Subcommands: `gputick collect`, `gputick serve`, `gputick version`.
- **Data format**: Parquet via `parquet-go`.
- **Storage**: Cloudflare R2 through its S3-compatible API, plus a local filesystem backend for development and tests.
- **Infrastructure**: Terraform under `infra/`. One small VM, target under $15/month.
- **Build**: `Makefile` targets `build`, `test`, `cover`, `lint`, `fmt`, `tidy`, `clean`. The version string is injected via `-ldflags "-X main.version=..."`.
- **CI**: GitHub Actions runs lint, test (with `-race`), and build on every push to `main` and every pull request.

## Code Style

- Follow standard Go conventions: `gofmt`, `goimports` (local prefix `github.com/HMetcalfeW/gputick`), short variable names in small scopes, clear naming in exported APIs.
- Prefer early returns over deep nesting.
- Keep functions focused: one responsibility per function. If a function exceeds about 50 lines, split it.
- Use `log/slog` for structured logging. Always include a `"func"` attribute naming the function, for example `slog.With("func", "collect.Run", "source", name)`. Do not use `fmt.Println` for diagnostics.
- Pass `context.Context` as the first argument to anything that does I/O. Every network call gets a timeout derived from the context.
- Wrap errors with `fmt.Errorf("...: %w", err)` so callers can use `errors.Is` and `errors.As`. Define sentinel errors as package-level `var Err... = errors.New(...)`.
- Group package-level declarations into a single `var ( ... )` block and a single `const ( ... )` block per file rather than several standalone `var x = ...` or `const y = ...` lines. When an individual item needs a why-comment, put it inside the block directly above that item.
- Do not add comments that restate what the code does. Only comment on **why** something is done when the reason is not obvious.
- Do not add docstrings, type annotations, or comments to code you did not change.
- Exported types in `pkg/schema/` are a public contract. Renaming or retyping a field there is a breaking change and must be called out explicitly in the change description and in `docs/SCHEMA.md`.

## Testing Expectations

### Coverage
- **Target**: 85%+ overall, no exported function below 80%.
- Run `make cover` (equivalent to `go test -race -coverprofile=coverage.out ./...` followed by `go tool cover -func=coverage.out`) to verify.
- CI runs the same commands on every push and pull request.

### Test Structure
- **Unit tests** go in `_test.go` files alongside the code they test, in the same package for internal functions or a `_test` package for black-box testing of exported APIs.
- **Contract tests** exist for every source under `internal/source/<name>/`. They replay a recorded fixture from `internal/source/<name>/testdata/` through the parser and assert on the resulting rows. They must fail loudly on upstream schema drift: a missing required field, a renamed column, or a changed enum is an error, not a warning.
- **Tests never hit the network.** HTTP sources are tested against `net/http/httptest` servers that serve the recorded fixture. File-based sources read the fixture from disk. A test that needs live credentials or a live endpoint is a bug.
- **Fixtures** are trimmed to the smallest input that still exercises every shape the parser handles, checked into `testdata/`, and kept under about 200KB each. Record the upstream URL and capture date in a comment in the test.
- **Normalization tests** in `internal/normalize/` are table-driven over raw rows and assert the full `Observation`, including `price_type`, form factor, GPU, and region.
- **Store tests** use the local filesystem backend and `t.TempDir()`. Do not test against R2.
- **CLI tests** build the root command, call `SetArgs()`, and capture output with `bytes.Buffer` via `SetOut()`/`SetErr()`.

### Test Quality
- Every new feature must include tests for both **positive** (correct parse, correct mapping) and **negative** (malformed input, unknown GPU, missing region, stale data) behavior.
- Use table-driven tests for functions with multiple input/output cases.
- Use `t.Run()` subtests for logical grouping within a test function.
- Use `require` for preconditions that must hold (test aborts on failure). Use `assert` for the actual assertions being tested.
- Use `t.Helper()` in test helper functions so failure messages point to the calling test.
- Use `t.Cleanup()` for teardown instead of `defer` in test helpers.
- Tests must be deterministic. Inject clocks and random sources; never depend on wall-clock time or map iteration order.

### Test Naming
- `TestFunctionName` for basic unit tests.
- `TestFunctionName_SpecificScenario` for scenario-specific tests.
- `TestSource_<Name>_Contract` for source contract tests (for example `TestSource_Lambda_Contract`).
- `TestCommand_<Subcommand>_<Scenario>` for CLI tests (for example `TestCommand_Collect_Once`).

## Architecture

### Package Layout
```
cmd/gputick/          CLI entrypoint (Cobra): collect, serve, version
internal/collect/     scheduler and worker pool; runs sources on a cadence with per-source timeouts
internal/source/      one package per source (skypilot, gpuhunt, awsspot, azure, vast, runpod, lambda, tensordock)
internal/normalize/   raw rows to Observation; GPU, form factor, price type and region taxonomies
internal/store/       raw snapshot and observation writers: Parquet, R2/S3, local filesystem
internal/rollup/      precomputed series served to the site and API
internal/api/         HTTP JSON API
internal/mcp/         MCP server
pkg/schema/           exported Go types (Observation, Source, Availability) importable by others
web/                  TypeScript chart-first site (later)
infra/                Terraform: modules/{compute,storage,site,secrets}, envs/
docs/                 METHODOLOGY.md, SCHEMA.md, PLAN.md
.github/workflows/    CI
```

- `cmd/` is a thin layer: parse flags, wire dependencies, call into `internal/`. No business logic.
- `internal/source/` packages know how to fetch and parse one upstream. They return raw rows and never import `normalize` or `store`.
- `internal/normalize/` owns the taxonomies. It is the only place that maps vendor strings to canonical GPU names, form factors, price types, and regions.
- `pkg/schema/` depends only on the stdlib. Anything in the module may import it; it imports nothing else in the module.

### Data Flow
```
source -> raw snapshot -> normalize -> observation -> store -> rollup -> api / mcp / site
```
1. `collect` runs each source on its cadence with a per-source timeout.
2. The raw response is written as a snapshot before any parsing. If normalization fails, the snapshot still exists and can be replayed.
3. `normalize` turns raw rows into `schema.Observation` values with `price_type`, form factor, GPU, region, and availability filled in.
4. `store` appends observations as Parquet, locally or to R2.
5. `rollup` precomputes the series the site and API serve, so queries never scan raw observations.
6. `api`, `mcp`, and `web/` read only from rollups.

### Adding a New Source
1. Create `internal/source/<name>/` and implement the `Source` interface defined in `internal/source/`. Keep fetch and parse separate so parse can be tested without I/O.
2. Record a fixture: fetch one real response, trim it to the minimum rows that cover every shape the parser handles, and save it under `internal/source/<name>/testdata/` with the capture date and URL noted in the test.
3. Write the contract test `TestSource_<Name>_Contract`. It must fail on schema drift and cover at least one malformed-input case.
4. Register the source in the collector registry in `internal/collect/` with its cadence and timeout.
5. Add normalization mappings in `internal/normalize/` for any new GPU names, form factors, price types, or regions this source introduces, with table-driven tests for each new mapping.
6. Document the source in the source table in `docs/METHODOLOGY.md`: what it measures, price type, cadence, known gaps.
7. Update the source table in `README.md`.

## Data Rules

These rules are not negotiable. A change that violates one is wrong even if every test passes.

- **No headless browsers.** Sources are HTTP APIs, published files, or plain HTML fetched with `net/http`. If a provider can only be read through a browser, it is not a source.
- **Never launch GPU instances to probe availability.** Availability comes from what providers publish (capacity flags, placement scores, marketplace listings), never from attempting to rent.
- **Every source has a contract test** that fails loudly on upstream schema drift. Silent partial parses are forbidden.
- **Raw snapshots are retained forever and never mutated.** Fix bugs by re-normalizing from snapshots, never by editing them.
- **Stale values are carried forward only with an explicit staleness flag.** A carried-forward value must never be indistinguishable from a fresh one.
- **Every observation carries `price_type` and form factor.** `price_type` is one of `rate_card`, `dynamic_spot`, `marketplace_ask`, `min_bid`, `executed_trade`, `reserved`, as defined in `docs/METHODOLOGY.md`. Form factor records SXM vs PCIe, NVLink, and node size (1x vs 8x). An observation missing either is rejected at normalization.
- **Data files are never committed to the repo.** Parquet files, snapshots, and anything under `data/` stay out of git. Small trimmed fixtures under `testdata/` are the only exception.
- Prices are stored with their currency and unit (per GPU-hour or per node-hour). Do not convert silently.
- Regions keep the provider's own identifier alongside the canonical region. Never discard the original.

## Infrastructure

- Terraform lives under `infra/` with reusable modules in `infra/modules/{compute,storage,site,secrets}` and environment roots in `infra/envs/`.
- Run `terraform fmt -recursive` and `terraform validate` before staging any `.tf` change.
- Keep modules small and single-purpose. A module that provisions compute does not also manage DNS.
- Remote state lives in R2 via the S3 backend. Never commit `*.tfstate` or `.terraform/`. Do commit `.terraform.lock.hcl`.
- No secrets in state, in variables files, or in the repo. Secrets are injected at apply time from the environment or a secrets manager. `*.tfvars` files are ignored except `*.example.tfvars`.
- The target is one small VM under $15/month. Any change that adds a billable resource must say what it costs.

## Git Workflow

- **Commit messages** use conventional commits: `feat`, `fix`, `test`, `ci`, `docs`, `refactor`, `chore`. Always include a scope, for example `feat(source): add lambda capacity collector` or `fix(normalize): map H100 NVL to its own form factor`.
- **Keep messages brief.** One line under 72 characters is the norm. Add a body only when the why is not obvious from the diff.
- **Commits are signed and signed off** (`git commit -S --signoff`). Work lands through pull requests from a feature branch, never directly on `main`.
- **The agent stages but never commits.** Stage changes with `git add`. Never run `git commit`, `git push`, `git tag`, `git merge`, or `git rebase`, and never open a pull request. The owner reviews the staged diff and commits.
- Never add `Co-Authored-By` or any other trailer to a commit message you propose.
- Lint (`make lint`) and tests (`make test`) must pass before staging. Do not stage a red tree.
- Releases are versioned via git tags and handled by the owner.

## What Not To Do

- Do not introduce new dependencies without justification. Prefer the stdlib. If you add a module, say in the change description why the stdlib does not suffice.
- Do not scrape with browsers, headless or otherwise.
- Do not launch instances, place orders, or do anything that costs money against a provider API.
- Do not commit data files, Parquet output, snapshots, or fixtures larger than about 200KB.
- Do not add feature flags or backward-compatibility shims. Change the code.
- Do not create helper utilities for one-time operations.
- Do not add error handling for scenarios that cannot happen.
- Do not modify files unrelated to the current task (no drive-by refactors).
- Do not create documentation files unless explicitly asked. Update `docs/METHODOLOGY.md`, `docs/SCHEMA.md`, and `README.md` when a change affects them; do not add new ones.
- Do not skip linting or disable a linter rule to make a build pass. Fix the code, or justify the `//nolint` directive with a specific linter and a reason.
- Do not commit scratch files, temporary output, or `.env` files.
- Do not commit, push, tag, merge, or open pull requests.
