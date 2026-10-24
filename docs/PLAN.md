# gputick build plan

Status: pre-alpha, day one, no data collected yet. Last updated 2026-10-24.

This is the working plan for gputick and the source for the GitHub Project board. Each milestone is meant to become one issue or a small cluster of issues. Companion documents: [README.md](../README.md) for the pitch, [AGENTS.md](../AGENTS.md) for coding standards, [docs/METHODOLOGY.md](METHODOLOGY.md) for how numbers are labeled and normalized, [docs/SCHEMA.md](SCHEMA.md) for the Observation and Source types.

## 1. Goal and non-goals

Goal: an open, auditable time series of GPU rental prices and availability, by provider and region, across hyperscalers and neoclouds. It sits on top of the catalogs that schedulers already maintain (SkyPilot catalog, dstack gpuhunt) and adds the dimensions nobody keeps: region-level history retained forever, availability as a first-class series, and honest labeling of what each number is.

Three differentiators:

1. Region-level history retained forever.
2. Availability as a first-class series. "Listed" versus "obtainable". Effective price is price weighted by the probability of obtaining the instance.
3. Every observation carries a `price_type` (`rate_card`, `dynamic_spot`, `marketplace_ask`, `min_bid`, `executed_trade`, `reserved`) and a form factor (SXM or PCIe, NVLink, node size 1x or 8x), with the normalization published in METHODOLOGY.md.

Non-goals:

- Not a scheduler. SkyPilot and dstack own that and become consumers of this data.
- Not a marketplace.
- Not a settlement index (Silicon Data, Ornn).
- Not a 65-provider comparison table. OpenComputePrices tried that, hit GitHub Actions timeouts with browser scrapers, and died in mid-2026.
- No headless browsers. Never launch GPU instances to probe availability.

Success criteria for the first year: one external citation, one PR merged upstream (for example the Lambda capacity flag into the SkyPilot catalog fetcher), and schedulers or agents querying the API.

## 2. Architecture overview

### Data flow

```
source (HTTP, S3, GitHub raw)
  -> raw snapshot        immutable Parquet, upstream columns preserved verbatim, partitioned by source and time
  -> normalize           per-source Normalizer applies GPU, form factor, price type and region taxonomies
  -> Observation         one row per (source, provider, region, gpu, form_factor, node_size, price_type) per run
  -> observation store   Parquet on R2, partitioned by date; local filesystem for development
  -> rollup              precomputed daily and hourly series as static JSON
  -> API / MCP / site    read rollups only; never touch raw in the request path
```

Raw snapshots are the asset. Everything downstream can be recomputed from them, so a normalization bug or a taxonomy change is a replay, not a data loss.

Partition layout on R2, defined in [SCHEMA.md](SCHEMA.md) section 5:

```
raw/source=<source>/date=YYYY-MM-DD/hour=HH/<run_id>.parquet
manifest/date=YYYY-MM-DD/<run_id>.json
obs/date=YYYY-MM-DD/<run_id>.parquet
rollup/<gpu_slug>/<provider>/<region_bucket>/daily.json
```

The upstream schema version is recorded in the raw envelope's `upstream_version` column rather than in the path.

### Packages

| Path | Responsibility |
|---|---|
| `cmd/gputick/` | Cobra entrypoint: `collect`, `serve`, `version`. Thin, delegates to `internal/`. |
| `internal/collect/` | Scheduler and worker pool. Runs registered sources concurrently on a cadence with a per-source timeout, writes the run manifest. |
| `internal/source/<name>/` | One package per source: `skypilot`, `gpuhunt`, `awsspot`, `azure`, `vast`, `runpod`, `lambda`, `tensordock`. Each owns its `Source`, its `Normalizer`, a recorded fixture and a contract test. |
| `internal/normalize/` | Shared taxonomies (GPU names, form factors, price types, region mapping) and the raw-row to `Observation` helpers the per-source normalizers call. |
| `internal/store/` | `Store` implementations: local filesystem and R2/S3. Parquet writing via parquet-go. |
| `internal/rollup/` | Reads observations, emits precomputed JSON series. Runs at the end of each collect cycle in the MVP. |
| `internal/api/` | HTTP JSON API over the rollups. |
| `internal/mcp/` | MCP server exposing the same queries as tools. |
| `pkg/schema/` | Exported types: `Observation`, `Source` metadata, `Availability`, `PriceType`. The only package other Go programs should import. |
| `web/` | TypeScript chart-first site. Later. |
| `infra/` | Terraform: `modules/{compute,storage,site,secrets}`, `envs/`. |
| `docs/` | METHODOLOGY.md, SCHEMA.md, PLAN.md. |
| `.github/workflows/` | CI: lint, test, build, release. |

### Key interfaces

Proposed shapes, to be finalized in code during Phase 1:

```go
// internal/source
type Source interface {
    Name() string
    Fetch(ctx context.Context) (RawResult, error)
}

type RawResult struct {
    Rows []RawRow   // map[string]string per row; upstream column names kept as-is
    Meta FetchMeta  // FetchedAt, UpstreamVersion, URL or key, ETag, Bytes, Duration
}

type Normalizer interface {
    Normalize(raw RawResult) ([]schema.Observation, []Issue, error)
}

// internal/store
type Store interface {
    WriteRaw(ctx context.Context, run RunID, source string, raw RawResult) error
    WriteObservations(ctx context.Context, run RunID, obs []schema.Observation) error
    WriteManifest(ctx context.Context, m RunManifest) error
}
```

Raw rows are kept as strings on purpose. Typing happens in the normalizer, so an upstream that changes a column's type or adds a column cannot break the raw write.

### How `gputick collect` runs

1. Load config and the source registry. A source is enabled when its required credentials are present; sources with missing keys are listed as `skipped` in the manifest, not treated as failures.
2. Open the `Store` (local path or R2 bucket from config).
3. Start a run with a `run_id` (UTC timestamp plus a short random suffix).
4. Fan out: each enabled source runs in its own goroutine under a per-source timeout (proposed default 60 seconds, configurable per source). A slow or broken source never blocks the others.
5. For each source that returns: write the raw snapshot first, then normalize, then write observations. A normalize failure still leaves the raw snapshot on disk.
6. Do not fabricate rows. A series the source did not report this cycle gets no observation, and observation files always have `stale=false`. Carry-forward happens only in rollups and API responses, for at most 72 hours with `stale=true` and `stale_since` set (METHODOLOGY.md section 9, following the ewyluda/gpu-price-index pattern).
7. Write the run manifest: run id, start and end time, per-source status (`ok`, `failed`, `skipped`; a timeout is `failed` with its error string), row counts (raw, observations, unmapped GPU rows), unmapped regions, upstream version and content hash, duration, binary and methodology versions. Fields are defined in SCHEMA.md section 4.
8. Ping healthchecks.io only if every enabled source either succeeded or was explicitly skipped. A failed source withholds the ping so the alert fires.
9. `--once` exits here. Default mode sleeps until the next cadence boundary (hourly, aligned to the top of the hour) and repeats. Rollups run after each cycle in the MVP.

## 3. Phases

Duration bands are guesses for a part-time solo developer. Phases 1 and 4 are meant to overlap: get the daemon onto a VM as soon as it can write a raw snapshot, because history compounds from day one and lost days cannot be recovered. Everything else can wait; collection cannot.

### Phase 0: Scaffold (this week, about 1 week)

- 0.1 Repo, licenses, docs skeleton (README, AGENTS, METHODOLOGY, SCHEMA, PLAN), DATA_LICENSE.
- 0.2 CI: golangci-lint, `go test ./...`, build, on push and PR.
- 0.3 CLI skeleton with `collect --once`, `serve`, `version`. Already stubbed.
- 0.4 `pkg/schema` first draft of `Observation` matching SCHEMA.md.

Acceptance: `go build ./... && golangci-lint run && go test ./...` is green in CI; `gputick version` prints an injected version; SCHEMA.md and `pkg/schema` agree field for field.

### Phase 1: Collector core and inherited upstreams (2 to 3 weeks)

- 1.1 `Source`, `RawResult`, `Store` interfaces and the local filesystem store writing Parquet.
- 1.2 SkyPilot catalog adapter: fetch the pinned `catalogs/v<N>/<cloud>/` CSVs over raw GitHub, one raw snapshot per cloud per run.
- 1.3 gpuhunt adapter: read `v3/<provider>/version` from the public `dstack-gpu-pricing` bucket in eu-west-1, fetch the zip, write one raw snapshot per provider CSV.
- 1.4 Run loop with per-source timeout, run manifest, `--once` and daemon modes.
- 1.5 R2 store (S3-compatible) behind the same `Store` interface.
- 1.6 healthchecks.io ping on a fully successful cycle.
- 1.7 Contract test per adapter against a recorded fixture, failing loudly on column changes.

Acceptance: `gputick collect --once` against the real upstreams writes raw Parquet for both sources to local disk and to an R2 bucket, emits a manifest, and pings healthchecks. A fixture with a renamed column makes the contract test fail.

### Phase 2: Own sources (4 to 6 weeks)

Priority order, each with a recorded fixture and a contract test:

- 2.1 Lambda instance-types API, keeping `regions_with_capacity_available` (free API key required).
- 2.2 RunPod keyless GraphQL: stock status and minimum bid price per GPU type.
- 2.3 AWS spot price history and spot placement scores (read-only IAM credentials).
- 2.4 Vast.ai: live offer count and asks.
- 2.5 Azure retail prices API (no auth).
- 2.6 TensorDock.
- 2.7 Later, outside the MVP: rate-card fetchers for CoreWeave and Crusoe, no browsers.

Acceptance per source: fixture-driven unit test, contract test, one live `--once` run that lands raw Parquet in R2, manifest shows a row count greater than zero, source listed in METHODOLOGY.md with its `price_type` and what "availability" means for it.

### Phase 3: Normalization, Observation table, rollups (3 to 4 weeks)

- 3.1 Taxonomies in `internal/normalize`: GPU canonical names, form factor, node size, price type, region mapping to a provider-neutral geography.
- 3.2 Per-source `Normalizer` for every Phase 1 and Phase 2 source.
- 3.3 Observation writer and partition layout.
- 3.4 Staleness carry-forward in rollups only, with `stale` and `stale_since` and the 72-hour limit from METHODOLOGY.md.
- 3.5 Replay (proposed `collect --replay <from> <to>`) that rebuilds observations from raw snapshots.
- 3.6 Daily and hourly rollups as static JSON, including an `effective_price` series where an availability signal exists.

Acceptance: a replay over all stored raw snapshots produces identical observations to the live path; unknown GPU strings fail the normalizer test rather than passing through silently; rollup JSON for one GPU renders in a scratch chart.

### Phase 4: Infrastructure (1 to 2 weeks, overlaps Phase 1)

- 4.1 `storage` module: R2 bucket, versioning on, lifecycle rules, API token scoped to the bucket.
- 4.2 `compute` module: one VM with cloud-init that installs the release binary and a systemd unit.
- 4.3 `secrets` module: SSM Parameter Store or sops, injected at boot.
- 4.4 `site` module: Cloudflare Pages project and DNS for gputick.dev.
- 4.5 Backend state in R2, two workspaces.
- 4.6 Monitoring: healthchecks.io check with grace period, alert to email.

Acceptance: `terraform apply` from a clean checkout produces a VM that completes a collect cycle within 15 minutes of boot with no manual steps; destroying and recreating the VM loses no data; total HCL under 200 lines.

### Phase 5: API and MCP server (2 to 3 weeks)

- 5.1 `gputick serve`: JSON endpoints over rollups (series by GPU, provider, region; latest snapshot; availability).
- 5.2 Static hosting of the same JSON on R2 so the MVP needs no running API server.
- 5.3 MCP server exposing the same queries as tools, with a `data_license` field in every response.

Acceptance: an agent configured with the MCP server answers "cheapest obtainable H100 SXM in a US region this week" from the data, with the price type and staleness visible in the response.

### Phase 6: Site (3 to 4 weeks)

- 6.1 TypeScript chart-first site on Cloudflare Pages reading rollup JSON.
- 6.2 Per-GPU page: price by provider and region over time, availability band, price type legend.
- 6.3 Methodology and attribution pages generated from the docs.

Acceptance: site loads with no backend, charts show real history, every chart states the price type and data license.

### Phase 7: Soak, review, mirror, upstream, announce (90 days of soak; 2 to 3 weeks of active work)

- 7.1 90 days of continuous history before announcing. The clock starts when Phases 1 and 4 land, not when Phase 6 does.
- 7.2 Methodology review: read every normalizer against METHODOLOGY.md, fix labels that overpromise.
- 7.3 Hugging Face dataset mirror of the public Parquet, synced on a schedule.
- 7.4 Upstream PRs: Lambda capacity flag into the SkyPilot catalog fetcher; a gpuhunt issue or PR where the data shows value.
- 7.5 Announce. Move the repo to a gputick org if a second contributor has not already forced it.

Acceptance: 90 days with no gap longer than one cadence in the manifest log for the two inherited upstreams; at least one upstream PR opened; DATA_LICENSE and attribution visible on the site, the Hugging Face card and the API.

## 4. Phase 0 and Phase 1 checklist

Issue-sized tasks. Paste into GitHub issues as they are picked up.

Phase 0:

- [x] Add `DATA_LICENSE` (CC-BY-4.0) next to `LICENSE` (Apache-2.0); add an attribution section for SkyPilot and gpuhunt to README.
- [x] Add `.github/workflows/ci.yml`: golangci-lint, `go test -race ./...`, `go build ./...`, on push and PR.
- [x] Add `.golangci.yml` and `.gitignore` (binaries, `data/`, `.env`, Terraform state and `.terraform/`).
- [x] Inject version via `-ldflags` from the `Makefile` build target. The release workflow is a Phase 1 task.
- [ ] Create `pkg/schema` with `Observation`, `PriceType`, `FormFactor`, `Availability`, matching SCHEMA.md; add a test that asserts the enum values.
- [ ] Create empty packages with `doc.go` for `internal/{collect,source,normalize,store,rollup,api,mcp}` so the layout is visible.
- [ ] Register gputick.dev. Record the registrar and renewal date in a private note, not the repo.
- [ ] Create the GitHub Project board from this document's phases.

Phase 1:

- [ ] Define `Source`, `RawResult`, `RawRow`, `FetchMeta` in `internal/source`.
- [ ] Define `Store` with `WriteRaw`, `WriteObservations`, `WriteManifest` in `internal/store`.
- [ ] Implement `store/fs`: Parquet writer via parquet-go, partition path builder, atomic rename on write.
- [ ] Implement `source/skypilot`: pin catalog version `v<N>`, list clouds, fetch each CSV over raw GitHub, record ETag and commit if available.
- [ ] Record SkyPilot fixtures (one trimmed CSV per cloud) and write a contract test asserting the column set.
- [ ] Implement `source/gpuhunt`: read `v3/<provider>/version`, fetch the zip, unzip in memory, one `RawResult` per provider CSV.
- [ ] Record gpuhunt fixtures and write a contract test asserting the 14 columns including `location` and `spot`.
- [ ] Implement `internal/collect`: registry, per-source timeout, errgroup fan-out, `RunManifest` type and writer.
- [ ] Implement `--once` and the daemon loop aligned to the top of the hour; graceful shutdown on SIGTERM that finishes the in-flight write.
- [ ] Implement `store/r2` using the S3-compatible API; integration test behind a build tag that runs only when R2 credentials are present.
- [ ] Add the healthchecks.io ping with the URL from config; skip silently when unset.
- [ ] Add structured logging with `source` and `run_id` fields on every line.
- [ ] Add `configs/example.yaml` (or env var documentation) listing every setting with defaults.
- [ ] Run `gputick collect --once` against the real upstreams and record the manifest shape in `docs/SCHEMA.md`. Commit no data.
- [ ] Add a release workflow that builds linux/amd64 and linux/arm64 binaries on tag so cloud-init has something to pull.

## 5. Infrastructure plan

### Tiers

| Tier | Shape | Monthly cost | Strengths | Weaknesses |
|---|---|---|---|---|
| 0 | GitHub Actions cron, R2, Pages | $0 to $2 | Nothing to patch, free | Cron is delayed under load, 6-hour job limit, this is what killed OpenComputePrices, no long-lived daemon |
| 1 (recommended) | One small VM running the daemon, R2, Pages | $4 to $15 | Reliable hourly cadence, cgo and DuckDB fine, simple to reason about, a place for provider API keys | One box, manual OS patching, no horizontal scale (not needed) |
| 2 | AWS serverless (EventBridge, Lambda, R2 or S3) | Comparable to Tier 1 at this volume (estimate) | No server, scales to zero | DuckDB cgo is awkward on Lambda, more IAM and HCL, cold starts, weaker local parity |
| 3 | Minute-level telemetry, ClickHouse or Timescale on a bigger VM | $15 to $60 | Ad hoc queries, high frequency for AWS, Vast, RunPod | Operational weight a solo maintainer does not need at hourly cadence |

Recommendation: Tier 1. The whole point is uninterrupted history, and Tier 0's cadence is the known failure mode. Tier 1 is the cheapest tier that gives a real scheduler loop and a home for API keys. Candidates: Hetzner CPX22 (about EUR 6.49), AWS t4g.small (about $12), Fly.io shared CPU ($3 to $5). Proposed default is Hetzner on price unless AWS is chosen for SSM and IAM convenience; decide in Phase 4.

Tier 0 remains a fallback for outages: a workflow that runs `gputick collect --once` on a schedule can be enabled by hand if the VM is down for more than a day.

### Terraform layout

```
infra/
  modules/
    compute/   VM, cloud-init (download release binary by tag, write env file, enable systemd unit), firewall
    storage/   R2 data bucket, versioning, lifecycle, scoped API token
    site/      Cloudflare Pages project, custom domain, DNS records
    secrets/   SSM parameters (AWS) or sops-encrypted file decrypted at boot
  envs/
    dev.tfvars
    prod.tfvars
  main.tf, variables.tf, backend.tf
```

State: S3-compatible backend pointing at a dedicated R2 bucket, separate from the data bucket. Expect to set the backend's compatibility flags for non-AWS endpoints. Two workspaces, `dev` and `prod`, selected with `terraform workspace select`, each reading its `envs/<ws>.tfvars`. `dev` may run with a tiny VM or none at all and point at a `gputick-dev` bucket. Target under 200 lines of HCL total.

### Secrets

Needed: R2 access key and secret for the daemon, Lambda API key, AWS IAM access key limited to `ec2:DescribeSpotPriceHistory` and `ec2:GetSpotPlacementScores`, Vast.ai key if required, healthchecks.io ping URL, Cloudflare API token for Terraform. On AWS use SSM Parameter Store with the instance role reading at boot. Elsewhere use sops with an age key; the encrypted file is committed, the age private key lives only on the owner's machine and the VM. Cloud-init writes `/etc/gputick/env` with mode 0600 and the systemd unit reads it with `EnvironmentFile`. No plaintext secrets in the repo.

### Cost estimate

| Item | Monthly |
|---|---|
| VM (Hetzner CPX22 or AWS t4g.small) | EUR 6.49 or about $12 |
| R2 storage, well under 15 GB in year one, zero egress | $0 to $1 |
| Cloudflare Pages | $0 |
| healthchecks.io free tier | $0 |
| gputick.dev domain | about $1 amortized (confirm at registrar) |
| Total | about $8 to $14 |

### Operations

- Health: one healthchecks.io check, period 1 hour, grace 30 minutes (proposed). The daemon pings only after a fully successful cycle, so partial failures alert within 90 minutes.
- Logs: journald on the VM, no log shipping for the MVP. The run manifests in R2 are the durable audit trail and are enough to answer "what happened at 03:00".
- Backups: not needed for raw snapshots, which are write-once and never overwritten. Turn bucket versioning on so an accidental delete or a buggy overwrite is recoverable. The Terraform state bucket is also versioned.
- Upgrades: the VM is stateless. Bump the release tag variable and `terraform apply`; cloud-init pulls the new binary. Rollback is the same operation with the previous tag.
- Lifecycle: no expiry on `raw/` or `obs/`. Optional 30-day expiry on noncurrent versions and on any `tmp/` prefix.
- Outage: healthchecks fires; if the VM cannot be fixed within a day, enable the Tier 0 fallback workflow to keep the hourly series alive.

## 6. Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Upstream schema drift (SkyPilot has bumped its catalog schema seven times in four years) | High | Medium | Pin schema version per adapter, contract test fails loudly, raw stored as untyped strings so the snapshot still lands, replay rebuilds observations after the fix |
| Source rot: pricing pages and APIs change weekly | High | Medium | One adapter per source, recorded fixtures, staleness flag so consumers see the gap, drop a source rather than keep a broken fetcher alive |
| API keys and rate limits | Medium | Medium | Hourly cadence is gentle, per-source timeout and backoff, sources with missing keys are skipped not failed, document each source's limits in METHODOLOGY.md |
| GitHub Actions cron unreliability if used for collection | High if used | High | Do not use it for the primary loop; Tier 1 VM; Actions only as a manual fallback |
| Overpromising "spot" where the provider only publishes a rate card | Medium | High for credibility | `price_type` is mandatory on every observation; a rate card is labeled `rate_card` even when the provider calls it spot; methodology review in Phase 7 |
| Legal and attribution for mirrored catalogs (Apache-2.0 SkyPilot, MPL-2.0 gpuhunt) | Low | Medium | Attribute in README, DATA_LICENSE, API responses and the Hugging Face card; keep source URLs in fetch metadata; engage both projects early |
| Solo-maintainer burnout from breadth (what killed the predecessors) | High | High | Nine GPUs and about twenty providers, no browsers, say no to provider number 21, collector before site, accept community collectors only after the contract-test harness exists |
| Data loss from a single VM | Low | High | All data written to R2 within the same cycle, VM holds nothing durable, bucket versioning on |
| Misreading availability signals (listed is not obtainable) | Medium | High for credibility | Separate `listed` and `obtainable` fields, `unknown` allowed, never infer obtainable from a rate card, document per-source semantics |

## 7. Decisions log

Short ADR-style entries. Dates are the day the decision was recorded.

**2026-10-23 Go over Python.** Context: the upstream catalogs and most predecessors are Python. Decision: Go for the collector, API and MCP server. Reason: single static binary for cloud-init, goroutines fit the fan-out loop, easy cross-compile for arm64 VMs, importable `pkg/schema` for other Go tools. Consequence: Python users consume Parquet and JSON, not a library; the site is TypeScript regardless.

**2026-10-23 Build on upstream catalogs rather than write collector number 40.** Context: SkyPilot catalog and gpuhunt already cover many providers with region columns but overwrite themselves. Decision: snapshot them hourly as inherited sources and write own collectors only where prices or capacity move. Consequence: coverage from day one; dependency on two upstreams, managed by adapters and contract tests.

**2026-10-23 Narrow scope: 9 GPUs, about 20 providers.** Context: OpenComputePrices died chasing 65 providers. Decision: cap the MVP at 9 GPU types and about 20 providers; expansion requires a maintainer, not an issue. Consequence: some requests will be declined.

**2026-10-23 Cloudflare R2 over S3 for public data.** Context: a public dataset is read far more than it is written. Decision: R2 for raw snapshots, observations, rollups and Terraform state. Reason: zero egress fees, S3-compatible API so the store code stays portable. Consequence: AWS-only conveniences (S3 event triggers, Athena) are unavailable; the Terraform backend needs compatibility flags.

**2026-10-23 Apache-2.0 code, CC-BY-4.0 data.** Decision: code under Apache-2.0, dataset under CC-BY-4.0 in `DATA_LICENSE`, attribution to SkyPilot and gpuhunt. Data is never committed to the repo. Consequence: anyone can use the data commercially with attribution; the license travels with the Parquet and the API.

**2026-10-23 Personal repo first, org later.** Decision: `github.com/HMetcalfeW/gputick` now; move to a `gputick` org at the second contributor or at announcement, whichever comes first. GitHub preserves redirects.

**2026-10-23 Name: gputick.** Decision: gputick, domain gputick.dev. Runner-up `computetick` kept in reserve if coverage grows past GPUs.

**2026-10-24 No headless browsers.** Context: browser scrapers pushed OpenComputePrices past the Actions time limit and broke constantly. Decision: HTTP APIs, raw files and plain HTML or JSON fetches only. A provider that cannot be read without a browser is out of scope.

**2026-10-24 No GPU probe launches.** Decision: availability comes only from provider-published signals (capacity flags, stock status, placement scores, offer counts). Never launch an instance to test obtainability. Reason: cost, terms of service, and the series would otherwise depend on the maintainer's wallet.

**2026-10-24 Collector first.** Decision: Phases 1 and 4 land before anything user-facing. Reason: history compounds; a site can be built on day 100, a missing day 1 cannot.

## 8. Open questions

1. **Should the collector require provider API keys?** Keys unlock Lambda capacity, Vast asks and RunPod detail but mean running from own infrastructure rather than Actions. Leaning: keys are optional per source and the daemon skips sources without them, so a keyless `--once` still works for contributors.
2. **Postgres hot window or static rollups only?** Static rollups cover the MVP charts and API. Postgres via pgx only if ad hoc queries become a real request; DuckDB over Parquet on the VM may be enough for maintainers.
3. **Engage SkyPilot and dstack early?** Leaning: open a short issue in each repo once Phase 1 lands, describing gputick as a consumer and offering the Lambda capacity flag PR. Earlier than that is noise.
4. **Which 9 GPUs?** Proposed: H100 SXM, H100 PCIe, H200, B200, A100 80GB SXM, A100 40GB, L40S, A10G, RTX 4090. Confirm before the taxonomy is written in Phase 3.
5. **Cadence for dynamic sources versus rate cards?** AWS spot, Vast and RunPod move within the hour; rate cards move monthly. Leaning: one hourly loop for everything in the MVP, with per-source cadence as a config field so dynamic sources can go to 15 minutes later without a redesign.
6. **Accept community collectors before the contract-test harness is solid?** Leaning: no. A collector PR needs a recorded fixture and a contract test, and the harness that makes that cheap is a Phase 2 deliverable.
7. **Hetzner, AWS or Fly for the VM?** Price says Hetzner, secrets convenience says AWS. Decide in Phase 4.
8. **Also keep the original response bytes next to the raw Parquet?** Proposed: yes, gzip the fetched payload beside the Parquet so a bug in the raw-to-Parquet step can be repaired. Storage is cheap; the cost is a few lines in the store.
