# gputick Data Schema

| | |
|---|---|
| Methodology version | 0.1.0 |
| Date | 2026-10-24 |
| Status | draft, pre-alpha, no data collected yet |
| Semantics | [docs/METHODOLOGY.md](METHODOLOGY.md) |
| Project overview | [README.md](../README.md), [docs/PLAN.md](PLAN.md), [AGENTS.md](../AGENTS.md) |

This document is the field-level contract for the artifacts gputick writes: raw snapshots, observations, run manifests and the planned rollups. [METHODOLOGY.md](METHODOLOGY.md) defines what the values mean; this document defines how they are named, typed and laid out. Where the two disagree, METHODOLOGY.md wins and this document has a bug.

## 1. Conventions

- Field names are `snake_case` and are used verbatim as Parquet column names and JSON keys.
- The Go types live in `pkg/schema` of module `github.com/HMetcalfeW/gputick`: `Observation`, `Source`, `Availability`, `RunManifest`. Each struct field is the CamelCase equivalent of the column name and carries both tags, for example:

  ```go
  PriceUSDPerGPUHour float64 `parquet:"price_usd_per_gpu_hour" json:"price_usd_per_gpu_hour"`
  ```

  Timestamp fields carry `parquet:"<name>,timestamp(microsecond)"`, and pointer fields are the nullable columns. `Observation` carries the three availability columns directly; `Availability` is a value type that `Observation.Availability()` returns and `SetAvailability` stores, used for validation and derivation. Embedding it was tried and dropped because parquet-go 0.32 reads null optional fields of an anonymous embedded struct back as non-nil zero values. `Source` describes a feed (slug, license, fetch method, auth, cadence) and backs the source table in METHODOLOGY section 10.
- Enumerations are typed strings in Go: `PriceType`, `AvailabilitySignal`, `FormFactor`, `Interconnect`, `PriceBasis`, `PriceStat`, `RegionBucket`, `RunStatus` and `SourceRunStatus`, each with a `Valid` method. Values marked proposed in METHODOLOGY.md (for example `oam`, or the `us` and `eu` fallback buckets) are not members until adopted.
- `Observation.Validate` and `RunManifest.Validate` enforce the required fields, enumeration membership, the `availability_value` encoding and `available` derivation in section 3, the staleness invariants, and the manifest rules in section 4. `Availability.DeriveAvailable` is the reference implementation of the derivation table. Normalizers must call `Validate` before writing a row.
- Types used below and their encodings:

| Type | Parquet | JSON |
|---|---|---|
| `string` | BYTE_ARRAY, UTF8 | string |
| `int32` | INT32 | number |
| `double` | DOUBLE | number |
| `bool` | BOOLEAN | boolean |
| `timestamp` | INT64, TIMESTAMP(MICROS), UTC-adjusted | RFC 3339 string in UTC with a `Z` suffix |

- Nullable fields are Parquet nulls and JSON `null`. Every field is always present in JSON; a missing key is a bug, not a null.
- Enumerations are lowercase strings and are never stored as integer codes.
- Parquet files use zstd compression.
- `run_id` identifies one collection cycle across all artifacts. Format: `<YYYYMMDD>T<HHMMSS>Z-<8 lowercase hex>`, for example `20261024T120000Z-3f9a1c2e`. The prefix is the run's `started_at` in UTC, so partitions are derived from the id alone. It sorts chronologically as a string and is safe in object keys. `schema.NewRunID` and `schema.RunIDTime` implement it, and `Validate` rejects ids in any other format.

## 2. Raw snapshot contract

A raw snapshot is what an upstream returned, re-encoded as Parquet with as little interpretation as possible. One file is written per source per run. Raw files are immutable and retained forever; they are the record from which everything else is derived and re-derived.

Every raw file begins with these envelope columns:

| Field | Type | Nullable | Description |
|---|---|---|---|
| `source` | string | no | Source slug (section 3, `source`). |
| `run_id` | string | no | The run that fetched it. |
| `fetched_at` | timestamp | no | When the upstream request completed. |
| `upstream_version` | string | yes | The upstream's own version string where one exists: SkyPilot catalog `v<N>` plus the commit SHA of the fetched file, the contents of the gpuhunt `version` file, an API version header. Null when the upstream exposes none. |
| `upstream_ref` | string | yes | The URL, S3 key or API operation name the record came from, with credentials removed. A source that calls more than one endpoint (for example `aws`: `ec2:DescribeSpotPriceHistory` and `ec2:GetSpotPlacementScores`) uses this column to tell records apart. |

Payload columns follow the envelope and are passed through verbatim:

- Tabular upstreams (CSV): one column per upstream column, named exactly as the upstream header, every value typed as `string`. No type inference, trimming, renaming or row filtering happens at this layer. An upstream column whose name collides with an envelope column, or with a name already assigned, is prefixed with `payload_` until it is free, and an empty header is named `unnamed_<n>` by its zero-based position. A repeated header cannot be a row key, so source adapters rename repeats with a `_<n>` suffix from the second occurrence on (`source.UniqueColumns`) before rows are built. When a source returns several parts (one file or endpoint each), their rows share one raw file and each row's `upstream_ref` and `fetched_at` come from its own part; a column one part lacks is null in that part's rows.
- JSON upstreams (REST and GraphQL APIs): one row per top-level record, with the record serialized verbatim into a single `payload_json` string column.
- HTML upstreams (planned scrapers): one row per extracted price cell, with the extracted text in `payload_text`, the selector that produced it in `selector`, and the SHA-256 of the fetched page body in `page_sha256`.

Raw files are written on every run even when the upstream body is unchanged. The manifest's `upstream_sha256` lets readers deduplicate.

## 3. Observation

One row per (series, run). Fields marked "proposed" go beyond the minimum agreed contract and may be renamed before the first data release.

| Field | Type | Nullable | Description |
|---|---|---|---|
| `observed_at` | timestamp | no | When gputick fetched the upstream; equals `fetched_at` of the raw file. Not the time the provider last changed the price. |
| `run_id` | string | no | Collection run. |
| `source` | string | no | Which feed produced the row. Slugs (proposed): `skypilot`, `gpuhunt`, `aws`, `azure-retail`, `lambda`, `runpod`, `vast`, `tensordock`, `coreweave-web`, `crusoe-web`. Go package names under `internal/source/` are identifiers and may differ from slugs, for example `awsspot` for `aws`. |
| `provider` | string | no | Lowercase provider slug: `aws`, `azure`, `gcp`, `lambda`, `runpod-secure`, `runpod-community`, `vast`, `tensordock`, `nebius`, `hyperstack`, `coreweave`, `crusoe`, and others as added. |
| `gpu_slug` | string | no | Canonical GPU identifier, METHODOLOGY section 6. |
| `gpu_vendor` | string | no | `nvidia`, `amd`. |
| `gpu_model` | string | no | Model segment of the slug: `h100`, `a100`, `mi300x`. |
| `form_factor` | string | no | `sxm`, `pcie`, `nvl`, `unknown` (`oam` proposed). |
| `gpu_memory_gb` | int32 | no | Marketed memory per GPU in GB. |
| `interconnect` | string | no | `nvlink`, `infiniband`, `ethernet`, `unknown`. |
| `node_gpu_count` | int32 | no | GPUs in the priced unit. |
| `instance_type` | string | yes | Provider's instance name verbatim (`p5.48xlarge`, `gpu_8x_h100_sxm5`). Null for marketplace offers that have no instance type. |
| `region_raw` | string | no | Provider's region string verbatim. Empty string when the listing has no region. |
| `zone_raw` | string | yes | Provider's finer location (AWS availability zone, RunPod datacenter id) when the price is specific to it. Proposed. |
| `region_bucket` | string | no | Coarse geography, METHODOLOGY section 7, including `global` and `unknown`. |
| `price_type` | string | no | `rate_card`, `dynamic_spot`, `marketplace_ask`, `min_bid`, `executed_trade`, `reserved`. |
| `tier` | string | no | Provider's own purchase-option label, lowercased with underscores: `on_demand`, `spot`, `preemptible`, `interruptible`, `community`. Keeps options with the same `price_type` apart. Proposed. |
| `price_stat` | string | no | Which statistic the price is: `list` for a single published number; `min` or `p50` across the marketplace offers in the group. Proposed. |
| `price_usd_per_gpu_hour` | double | no | Normalized price. |
| `price_basis` | string | no | `per_gpu_hour` or `per_node_hour`, the basis the source published. |
| `price_original` | double | no | The number as published, in `price_basis` and `currency_original`. |
| `currency_original` | string | no | ISO 4217 code; `USD` when no conversion occurred. |
| `fx_rate_to_usd` | double | yes | Rate applied when `currency_original` is not `USD`; null otherwise. Proposed. |
| `min_commitment_hours` | int32 | no | 0 when none; the term length for `reserved`. |
| `bundled` | bool | no | True when the price includes storage, egress or other resources the provider does not itemize. |
| `availability_signal` | string | no | `none`, `capacity_flag`, `stock_level`, `offer_count`, `placement_score`, `inferred`. |
| `availability_value` | string | yes | The signal's value encoded as a string, see below. Null when `availability_signal` is `none`. |
| `available` | bool | yes | Derived yes or no; null whenever the signal does not justify one. See below. |
| `stale` | bool | no | Always `false` in `obs/` files. May be `true` in rollups and API responses that carry a value forward. |
| `stale_since` | timestamp | yes | `observed_at` of the last real observation when `stale` is true; null otherwise. |
| `methodology_version` | string | no | METHODOLOGY.md version that produced the row, for example `0.1.0`. |
| `upstream_version` | string | yes | Copied from the raw envelope. |

### availability_value encoding

`availability_value` is a single string column so that every signal fits one Parquet type and one JSON type, and so that adding a signal never adds a column. The encoding is fixed per signal:

| availability_signal | availability_value | available |
|---|---|---|
| `none` | null | null |
| `capacity_flag` | `true` or `false` | same as the value |
| `stock_level` | provider's ordinal, lowercased (`high`, `medium`, `low`, `none`; vocabulary to verify) | `false` when `none`, otherwise `true` |
| `offer_count` | decimal integer, no sign, no separators (`212`) | `true` when greater than 0 |
| `placement_score` | decimal integer 1 to 10 (`7`) | null |
| `inferred` | defined by the named method in METHODOLOGY.md | defined by the method |

Readers that want numbers parse the string according to `availability_signal`. The API returns the same string.

### Series identity

Within one run, the tuple (`source`, `provider`, `gpu_slug`, `instance_type`, `node_gpu_count`, `region_raw`, `zone_raw`, `price_type`, `tier`, `price_stat`) is unique. Across runs the same tuple identifies one series. Rollups are keyed by a subset of it and must never merge rows that differ in `source`, `price_type`, `form_factor`, `node_gpu_count` or `tier`.

## 4. RunManifest

One JSON document per run, written after every source has finished or failed.

| Field | Type | Nullable | Description |
|---|---|---|---|
| `run_id` | string | no | As above. |
| `started_at` | timestamp | no | Run start. |
| `finished_at` | timestamp | no | Run end. |
| `collector_version` | string | no | Output of `gputick version` for the binary that ran. |
| `methodology_version` | string | no | Version applied when deriving observations. |
| `status` | string | no | `ok` (every source ok), `partial` (some failed), `failed` (no observations written). |
| `observations_path` | string | yes | Object key of the `obs/` file; null when `status` is `failed`. |
| `observations_total` | int32 | no | Rows in the observations file. |
| `sources` | array | no | One entry per attempted source, fields below. |

Each entry of `sources`:

| Field | Type | Nullable | Description |
|---|---|---|---|
| `source` | string | no | Source slug. |
| `status` | string | no | `ok`, `failed`, `skipped` (disabled by configuration). |
| `started_at` | timestamp | no | |
| `finished_at` | timestamp | no | |
| `duration_ms` | int32 | no | |
| `upstream_version` | string | yes | As in the raw envelope. |
| `upstream_sha256` | string | yes | SHA-256 of the fetched body, or of the concatenated bodies for multi-endpoint sources, for deduplication. |
| `raw_path` | string | yes | Object key of the raw file; null when `failed`. |
| `rows_raw` | int32 | no | Payload rows written to raw. |
| `rows_observations` | int32 | no | Observations derived from this source. |
| `rows_unmapped_gpu` | int32 | no | Raw rows dropped because the GPU could not be mapped to a slug. |
| `unmapped_regions` | array of string | no | Distinct `region_raw` values that mapped to `unknown`. Proposed. |
| `error` | string | yes | Error message when `failed`; null otherwise. Must not contain credentials. |

## 5. Storage layout

All artifacts live in one Cloudflare R2 bucket. Partition keys use Hive style (`key=value`) so DuckDB, Spark and similar readers discover them without configuration. `date` and `hour` are taken from the run's `started_at` in UTC.

```
raw/source=<source>/date=YYYY-MM-DD/hour=HH/<run_id>.parquet
manifest/date=YYYY-MM-DD/<run_id>.json
obs/date=YYYY-MM-DD/<run_id>.parquet
rollup/<gpu_slug>/<provider>/<region_bucket>/daily.json
rollup/<gpu_slug>/<provider>/<region_bucket>/hourly.json      (planned)
```

File naming:

- One raw file per source per run, one observations file per run, one manifest per run. File names are `<run_id>.parquet` or `<run_id>.json`; several runs may fall in the same `hour=` partition and stay distinct by `run_id`.
- Rollup file names are fixed (`daily.json`, `hourly.json`) and are overwritten on each rebuild.
- Every path component is lowercase ASCII with no spaces. `gpu_slug`, `provider` and `region_bucket` appear in paths exactly as they appear in the columns.

Mutability:

- `raw/` and `manifest/` are write-once. An object key is never overwritten.
- `obs/` is derived. On a methodology bump the files are re-derived from raw and overwritten in place under the same key; the `methodology_version` column identifies which derivation a file came from. Keeping superseded derivations under `obs-superseded/methodology=<version>/...` is proposed and not decided.
- `rollup/` is rebuilt from `obs/` and is the only place `stale = true` appears. Each rollup point carries its `source`, and a rollup never blends sources.
- Data license is CC-BY-4.0. Raw snapshots mirrored from the SkyPilot catalog (Apache-2.0) and gpuhunt (MPL-2.0) retain their upstream license and attribution; see METHODOLOGY section 13.

## 6. Example rows

Illustrative values, not collected data. Constant fields are omitted from the table: `run_id = 20261024T120000Z-3f9a1c2e`, `observed_at = 2026-10-24T12:00:04Z`, `methodology_version = 0.1.0`, `stale = false`, `stale_since = null`, `fx_rate_to_usd = null`.

| Field | A: rate card via SkyPilot | B: AWS spot with placement score | C: Vast marketplace ask | D: Lambda capacity flag |
|---|---|---|---|---|
| `source` | `skypilot` | `aws` | `vast` | `lambda` |
| `provider` | `lambda` | `aws` | `vast` | `lambda` |
| `gpu_slug` | `nvidia-h100-sxm-80gb` | `nvidia-h100-sxm-80gb` | `nvidia-rtx4090-24gb` | `nvidia-h100-sxm-80gb` |
| `gpu_vendor` / `gpu_model` | `nvidia` / `h100` | `nvidia` / `h100` | `nvidia` / `rtx4090` | `nvidia` / `h100` |
| `form_factor` | `sxm` | `sxm` | `pcie` | `sxm` |
| `gpu_memory_gb` | 80 | 80 | 24 | 80 |
| `interconnect` | `unknown` | `ethernet` | `unknown` | `unknown` |
| `node_gpu_count` | 8 | 8 | 1 | 8 |
| `instance_type` | `gpu_8x_h100_sxm5` | `p5.48xlarge` | null | `gpu_8x_h100_sxm5` |
| `region_raw` | `us-west-1` | `us-east-1` | `Texas, US` (format to verify) | `us-east-1` |
| `zone_raw` | null | `us-east-1a` | null | null |
| `region_bucket` | `us-west` | `us-east` | `us-central` | `us-east` |
| `price_type` | `rate_card` | `dynamic_spot` | `marketplace_ask` | `rate_card` |
| `tier` | `on_demand` | `spot` | `on_demand` | `on_demand` |
| `price_stat` | `list` | `list` | `min` | `list` |
| `price_basis` | `per_node_hour` | `per_node_hour` | `per_gpu_hour` | `per_node_hour` |
| `price_original` | 23.92 | 33.28 | 0.34 | 23.92 |
| `currency_original` | `USD` | `USD` | `USD` | `USD` |
| `price_usd_per_gpu_hour` | 2.99 | 4.16 | 0.34 | 2.99 |
| `min_commitment_hours` | 0 | 0 | 0 | 0 |
| `bundled` | false | false | false | false |
| `availability_signal` | `none` | `placement_score` | `offer_count` | `capacity_flag` |
| `availability_value` | null | `7` | `212` | `false` |
| `available` | null | null | true | false |
| `upstream_version` | `vN/a1b2c3d` | null | null | null |

Rows A and D describe the same product from two sources. A carries a region because the SkyPilot Lambda fetcher emits one row per region from a hardcoded list; it says nothing about capacity. D comes from Lambda's own API and says that region currently has none. They are separate series and a rollup must not merge them. Row B's `available` is null even though a score is present, because a placement score is a prediction, not an observation of capacity. Row C's `interconnect` is `unknown` because the marketplace listing states nothing about fabric; the AWS `p5.48xlarge` row is `ethernet` because its advertised inter-node fabric (EFA) is Ethernet-based.

## 7. JSON representation

The API and the rollup files use the same field names and encodings as the Parquet columns. Endpoint paths are out of scope here; see [docs/PLAN.md](PLAN.md). One observation (row D above):

```json
{
  "observed_at": "2026-10-24T12:00:04Z",
  "run_id": "20261024T120000Z-3f9a1c2e",
  "source": "lambda",
  "provider": "lambda",
  "gpu_slug": "nvidia-h100-sxm-80gb",
  "gpu_vendor": "nvidia",
  "gpu_model": "h100",
  "form_factor": "sxm",
  "gpu_memory_gb": 80,
  "interconnect": "unknown",
  "node_gpu_count": 8,
  "instance_type": "gpu_8x_h100_sxm5",
  "region_raw": "us-east-1",
  "zone_raw": null,
  "region_bucket": "us-east",
  "price_type": "rate_card",
  "tier": "on_demand",
  "price_stat": "list",
  "price_usd_per_gpu_hour": 2.99,
  "price_basis": "per_node_hour",
  "price_original": 23.92,
  "currency_original": "USD",
  "fx_rate_to_usd": null,
  "min_commitment_hours": 0,
  "bundled": false,
  "availability_signal": "capacity_flag",
  "availability_value": "false",
  "available": false,
  "stale": false,
  "stale_since": null,
  "methodology_version": "0.1.0",
  "upstream_version": null
}
```

Rollup files (planned) hold one series per file with a `points` array. Each point is a reduced observation that keeps the identity and labeling fields above and adds the aggregation window; the exact point shape is proposed in [docs/PLAN.md](PLAN.md) and will be added here before rollups ship. Points are the only place `stale` may be `true`.

## 8. Compatibility and evolution

Versions follow METHODOLOGY.md. The pre-1.0 semver exemption is not used; the rules below apply from 0.1.0.

- **Patch (0.1.0 to 0.1.1).** Editorial changes only. No derived value changes, no new columns, no re-derivation.
- **Minor (0.1 to 0.2).** Additive only. New nullable columns, new enumeration values, new slugs, new region mappings and new sources may be added. Existing columns are never renamed, retyped, removed or given a new meaning. Derived values for existing rows may change only where they were previously `unknown` or unmapped. Observations are re-derived so history gains the additions, and files written before the bump stay readable by the same readers.
- **Major (0.x to 1.0, 1.x to 2.0).** Breaking. A column or enumeration value may be renamed, removed, retyped or redefined, or a normalization rule may change the value of existing rows. All `obs/` files are re-derived from `raw/` and readers must update. The METHODOLOGY changelog entry names every field affected.
- Readers must ignore columns they do not know and must not depend on column order.
- The raw envelope (section 2) is additive only at every version. Payload columns follow the upstream and can change whenever the upstream changes; that is why `upstream_version` is recorded and why collectors pin the upstream schema and fail on an unexpected header rather than guessing.
- `run_id` format, partition layout and file naming are frozen within a major version.
