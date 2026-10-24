# gputick

Open time series of GPU rental prices and availability across clouds and neoclouds, by provider and region. Go collector, Parquet dataset, API, and MCP server.

**Status:** pre-alpha, day one. The CLI skeleton exists. No data has been collected yet, and there is no public dataset, API, site, or MCP server. Everything listed under Outputs below is planned.

## What this is

gputick records what GPUs cost to rent and whether you can actually get one, per provider and per region, and keeps that record forever. It sits on top of the catalogs that schedulers already maintain (the SkyPilot catalog and dstack's gpuhunt) and adds the dimensions nobody keeps: region-level history that is never overwritten, availability as a first-class series, and an honest label on every number saying what kind of price it is.

## Why

Several projects already publish GPU prices. After cloning and reading eight of them in October 2026, the gaps were consistent:

- Index projects flatten every neocloud to one global price and keep days of history, not years.
- Scheduler catalogs (SkyPilot, gpuhunt) have region columns but overwrite themselves on every refresh, so there is no history. Their spot columns are mostly empty. RunPod has zero spot rows.
- Nobody tracks availability over time. An Azure spot price at 70% off that is never actually obtainable looks identical to one you can get.
- Public trackers disagree with each other by up to 8x for the same GPU, because they blend rate cards, marketplace asks, and executed trades without saying which is which, and they mix SXM with PCIe and single cards with 8x nodes.

gputick exists to close those gaps and nothing else.

## What makes it different

1. **Region-level history, retained forever.** Every raw snapshot is stored as fetched and never deleted. Prices are kept per provider and per region, not averaged into one number.
2. **Availability as a first-class series.** Listed price and obtainable price are different things. gputick records availability signals alongside price so that an effective price, the listed price weighted by the probability of actually getting the machine, can be derived. That formula will be published in the methodology before it is shown anywhere.
3. **Every observation is labeled.** Each row carries a price type (`rate_card`, `dynamic_spot`, `marketplace_ask`, `min_bid`, `executed_trade`, `reserved`) and a form factor (SXM or PCIe, NVLink, 1x or 8x node). The normalization rules are published in [docs/METHODOLOGY.md](docs/METHODOLOGY.md) so anyone can check or dispute them.

## What it is not

| Not this | Who does that |
|---|---|
| A scheduler | SkyPilot and dstack. They are consumers of this data, not competitors. |
| A marketplace or reseller | Shadeform. |
| A settlement or benchmark index | Silicon Data and Ornn, which have futures contracts and Bloomberg distribution. |
| A 65-provider comparison table | OpenComputePrices tried that and shut down in mid-2026. |

## Data sources

All sources are planned. None are collecting yet.

Two upstream catalogs are snapshotted hourly and mirrored as-is. gputick runs its own collectors only where prices or capacity actually move, plus a few rate-card scrapers for providers neither catalog covers. No headless browsers. gputick never launches GPU instances to probe availability.

| Source | What we take | Price types present | Availability signal | Cadence |
|---|---|---|---|---|
| [SkyPilot catalog](https://github.com/skypilot-org/skypilot-catalog) (Apache-2.0) | Raw CSVs for about 20 neoclouds with regions, including Lambda, RunPod, Nebius, Hyperstack, OVH, and Scaleway | `rate_card`; `dynamic_spot` for AWS and Azure where `SpotPrice` is populated, which is rare for neoclouds | None | Hourly snapshot |
| [dstack gpuhunt](https://github.com/dstackai/gpuhunt) (MPL-2.0) | Public S3 bucket: a version file plus a zip with one CSV per provider, with location and spot fields | `rate_card`; `dynamic_spot` for AWS and Azure where `spot` is set | None | Hourly snapshot |
| AWS | Spot price history and spot placement scores | `dynamic_spot`, `rate_card` | Spot placement scores | Hourly |
| Azure | Retail Prices API | `rate_card`, `dynamic_spot` | None from this API | Hourly |
| Vast.ai | Live offers | `marketplace_ask`; `min_bid` for interruptible offers | Offer counts | Hourly |
| RunPod | Keyless GraphQL API | `rate_card` (secure cloud), `marketplace_ask` (community cloud), `min_bid` (spot) | Stock status | Hourly |
| Lambda | Instance types API | `rate_card` | `regions_with_capacity_available`, a field SkyPilot discards | Hourly |
| TensorDock | Marketplace listings | `marketplace_ask` (API details to verify) | Offer counts | Hourly |
| Rate-card scrapers (CoreWeave, Crusoe, others as needed) | Published price pages | `rate_card` | None | Hourly |

No planned source provides `executed_trade`, and `reserved` prices are not collected in the MVP. Both labels exist so the taxonomy is complete, and so a future source of real trades or committed-term prices can be labeled correctly instead of being passed off as a list price.

## Outputs

All planned, in build order. None exist yet.

1. **Parquet dataset** on Cloudflare R2: raw snapshots plus normalized observations, public under CC-BY-4.0.
2. **JSON API** over the normalized series and precomputed rollups.
3. **MCP server**, so agents and schedulers can ask questions like "where is an H100 obtainable under $2.50 right now".
4. **Chart-first site**: pick a GPU, see price and availability over time across providers and regions.
5. **Alerts**, later.

The collector comes first because history compounds from day one and lost days cannot be recovered.

## Quickstart

Requires Go 1.26 or newer.

```sh
git clone https://github.com/HMetcalfeW/gputick.git
cd gputick
make build
./bin/gputick version
./bin/gputick collect --once
```

Without `make`, `go build -o bin/gputick ./cmd/gputick` is equivalent to `make build`.

The collector is a stub today. `gputick collect --once` and `gputick serve` return `not implemented yet` and exit non-zero. `gputick version` works.

## Repository layout

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

Only `cmd/gputick/` exists today. The other directories are created as each phase lands.

Data is never committed to this repository. Code, methodology, and small daily rollups live here. Raw snapshots live in R2.

## Methodology and schema

- [docs/METHODOLOGY.md](docs/METHODOLOGY.md) explains how raw rows become observations: the GPU, form factor, price type, and region taxonomies, how effective price is derived from availability, and what is deliberately not normalized. This is the document we want people to cite.
- [docs/SCHEMA.md](docs/SCHEMA.md) defines the Parquet and API schema: the `Observation`, `Source`, and `Availability` types in `pkg/schema/`, column by column.

If a number in the dataset looks wrong, the methodology is the first place to look, and the first place to file an issue against.

## Roadmap

[docs/PLAN.md](docs/PLAN.md) has the full plan, with milestones, acceptance checks, and an open-questions list. In one line per phase:

0. Scaffold: repo, licenses, docs, CI, CLI skeleton, first draft of `pkg/schema`.
1. Collector core: `Source` and `Store` interfaces, SkyPilot and gpuhunt adapters, raw Parquet snapshots to local disk and R2, run manifest.
2. Own sources, in priority order: Lambda capacity, RunPod, AWS spot history and placement scores, Vast.ai, Azure retail, TensorDock.
3. Normalization: taxonomies, the Observation table, staleness flags, replay from raw, daily and hourly rollups.
4. Infrastructure: Terraform modules, one VM, R2 lifecycle, secrets, monitoring. Overlaps Phase 1 so collection starts early.
5. API and MCP server over the rollups.
6. Chart-first site.
7. 90-day soak, methodology review, Hugging Face mirror, upstream pull requests, announce.

MVP scope is 9 GPU types, about 20 providers, hourly cadence, and 90 days of retained history before announcing anything.

Success looks like: one external citation of the methodology or dataset, one pull request merged upstream into SkyPilot or dstack (for example, adding the Lambda capacity flag), and schedulers or agents querying the API.

## Contributing

Pre-alpha means the shape of things will change. Issues and small, focused pull requests are welcome.

- [AGENTS.md](AGENTS.md) holds the coding standards: Go 1.26, golangci-lint v2, testify, and the `make build`, `make test`, `make lint`, and `make cover` targets that CI runs.
- Every collector must come with a contract test: a recorded upstream response checked into the source package and a test that asserts the exact observations produced from it. This is how we notice when a provider changes its API or its price page.
- No headless browsers. Never launch GPU instances to probe availability.
- Keep pull requests to one change. A new source, a taxonomy fix, and a storage refactor are three pull requests.

## License

- **Code** is licensed under Apache-2.0. See [LICENSE](LICENSE).
- **Data** is licensed under CC-BY-4.0. See [DATA_LICENSE](DATA_LICENSE).

Snapshots mirror the [SkyPilot catalog](https://github.com/skypilot-org/skypilot-catalog) (Apache-2.0) and [dstack gpuhunt](https://github.com/dstackai/gpuhunt) (MPL-2.0), and those projects are credited in the dataset. Raw provider prices are facts and carry no license of their own.
