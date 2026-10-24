# gputick Methodology

| | |
|---|---|
| Version | 0.1.0 |
| Date | 2026-10-24 |
| Status | draft, pre-alpha, no data collected yet |
| Field contract | [docs/SCHEMA.md](SCHEMA.md) |
| Project overview | [README.md](../README.md), [docs/PLAN.md](PLAN.md), [AGENTS.md](../AGENTS.md) |

This document defines what a gputick observation means. It is the document to cite when you use the data. Every observation carries the version of this document that produced it in `methodology_version`. Any change to the taxonomies, normalization rules or staleness policy below bumps the version and triggers a re-derivation of observations from raw snapshots (section 12).

## 1. Purpose

gputick is an open, auditable time series of GPU rental prices and availability, by provider and region, across hyperscalers and neoclouds. It builds on the catalogs that schedulers already maintain (the SkyPilot catalog and dstack's gpuhunt) and adds three things they do not keep: region-level history retained forever, availability as a first-class series, and an explicit label on every number saying what kind of price it is.

Public GPU price trackers disagree with each other by up to 8x. Most of that spread is not measurement error. It comes from blending numbers that are not comparable: published rate cards, marketplace asks, standardized benchmarks and executed trades, mixed across SXM and PCIe form factors, NVLink and non-NVLink nodes, single GPUs and 8-GPU nodes, bundled and itemized storage and egress, InfiniBand and plain Ethernet, minimum commitments and regions. gputick does not try to resolve that spread into one number. It labels every observation so that a reader can decide which observations are comparable.

Two constraints shape everything below. gputick never launches GPU instances to probe availability, so every availability signal is one the provider publishes. And gputick never fabricates rows: if a source did not report a series in a cycle, there is no observation for it in that cycle.

## 2. What an observation is

An observation is one row stating: at time `observed_at`, source S reported that provider P listed GPU G in region R at price X per GPU-hour, where X is a price of kind `price_type`, together with whatever availability signal S exposes for that series. One observation is written per collection cycle for every series the source reports in that cycle. An observation records what a source published, not what anyone paid.

Three properties follow:

- An observation never combines two sources. If Lambda is reported by both the SkyPilot catalog and Lambda's own API, those are two observations with different `source` values, and rollups must not blend them.
- An observation never averages across price types, form factors, node sizes or tiers. Each distinct combination is its own series.
- An observation with `availability_signal = none` says nothing about whether the machine could be rented. A rate card with a region label is not evidence that capacity exists in that region.

Raw upstream responses are stored verbatim and retained forever. Observations are derived from raw and can be re-derived when this document changes. The field-level contract is in [docs/SCHEMA.md](SCHEMA.md).

## 3. Price types

`price_type` is the most important label in the dataset. Values are never mixed within an index, average or rollup.

| price_type | What the number is | Sources producing it |
|---|---|---|
| `rate_card` | A published list price, including fixed-discount "spot" tiers | SkyPilot, gpuhunt, Azure retail (on-demand), Lambda, RunPod secure, CoreWeave and Crusoe pricing pages (planned) |
| `dynamic_spot` | A provider-set price that moves with supply and demand | AWS spot, Azure spot (direct, and via SkyPilot or gpuhunt) |
| `marketplace_ask` | A seller's current asking price on a marketplace | Vast.ai, TensorDock, RunPod community |
| `min_bid` | The minimum bid a provider will currently accept | RunPod spot, Vast.ai interruptible (to verify) |
| `executed_trade` | A price at which a trade actually cleared | None today |
| `reserved` | A committed-term price, with its term length | None in the MVP |

**rate_card.** The price a provider publishes for on-demand use. It changes when the provider edits its pricing page, which for most neoclouds is rarely. GCP "spot" is included here deliberately: it is a fixed discount off on-demand that changes at most monthly, so it behaves like a list price, not a market price. Most neoclouds have no spot tier at all, only a rate card. Rate cards are the majority of what public trackers show and the majority of what gputick collects. A rate card says what a provider would charge if it had capacity; it does not say that it has any.

**dynamic_spot.** A price set by the provider's own pricing engine in response to spare capacity. Among hyperscalers this exists only at AWS and Azure. AWS publishes per availability zone history; Azure publishes a per-region spot price through its retail prices API. Azure spot often shows 70 to 80 percent discounts while capacity is frequently unobtainable, so a dynamic_spot price without an availability signal should be read with care.

**marketplace_ask.** The price an individual seller is asking right now on a marketplace of independent hosts. Many asks exist for one GPU in one region at one time. gputick does not pick one; it records distribution statistics (section 8, rule 9) together with the number of offers. RunPod community cloud is treated as a marketplace because the hosts are third parties, and is modeled as the separate provider `runpod-community`.

**min_bid.** The lowest bid the provider or marketplace will currently accept for interruptible capacity, where that floor is exposed directly. It is a floor, not a clearing price, and must not be compared with on-demand asks.

**executed_trade.** The price of a trade that actually cleared, for example on a compute exchange such as SF Compute. gputick has no source for executed trades today and says so in the source table. Executed trades are never mixed with listings in any index.

**reserved.** A committed-term price, recorded with the term length in `min_commitment_hours`. Not collected in the MVP.

## 4. Availability signals

`availability_signal` names the kind of signal, `availability_value` carries its value, and `available` is a derived boolean that is null whenever the signal does not justify a yes or no.

| availability_signal | Encoding of `availability_value` | `available` derivation | Sources |
|---|---|---|---|
| `none` | null | null | SkyPilot, gpuhunt, Azure retail, CoreWeave, Crusoe |
| `capacity_flag` | `true` or `false` | the flag | Lambda |
| `stock_level` | provider's ordinal, lowercased (`high`, `medium`, `low`, `none`; vocabulary to verify) | `false` when `none`, otherwise `true` | RunPod |
| `offer_count` | non-negative integer | `true` when greater than 0 | Vast.ai, TensorDock |
| `placement_score` | integer 1 to 10 | null (a prediction is not an observation of capacity) | AWS |
| `inferred` | defined per method | defined per method | none today |

All signals are provider-reported and coarse. A capacity flag is a point-in-time boolean with no quantity. A stock level is an ordinal whose thresholds are not published. An offer count says how many hosts are asking, not whether any of them would pass a reader's own filters. A placement score is AWS's prediction of fulfillment likelihood for a hypothetical request, not a count of free machines. The `inferred` value is reserved for signals gputick derives itself; none exist in 0.1.0, and any future method must be named in this document before it is used.

## 5. Form factor, interconnect and node normalization

Form factor and interconnect are two of the largest sources of disagreement between trackers, so they are first-class fields and are never averaged across.

| Field | Values | Meaning |
|---|---|---|
| `form_factor` | `sxm`, `pcie`, `nvl`, `unknown`, `oam` (proposed) | The GPU module type. `nvl` is NVLink-bridged PCIe (for example H100 NVL pairs). `oam` is proposed for AMD OAM modules such as MI300X; until adopted those records use `unknown`. |
| `interconnect` | `nvlink`, `infiniband`, `ethernet`, `unknown` | The fabric the listing states. `infiniband` when an inter-node InfiniBand fabric is stated; `nvlink` when intra-node NVLink is stated and no inter-node fabric is; `ethernet` when only Ethernet or RoCE is stated; `unknown` when the source says nothing. Intra-node NVLink is already implied by `sxm` and `nvl`, so in practice `interconnect` carries inter-node information. |
| `node_gpu_count` | positive integer, typically 1, 2, 4 or 8 | The number of GPUs in the priced unit. |
| `price_basis` | `per_gpu_hour`, `per_node_hour` | The basis in which the source published the price. |

Every price is normalized to USD per GPU-hour: when `price_basis` is `per_node_hour`, `price_usd_per_gpu_hour` is the USD node price divided by `node_gpu_count`. The original number and basis are kept in `price_original` and `price_basis`. Normalizing to a per-GPU basis does not make a 1-GPU offer comparable with an 8-GPU node; `node_gpu_count` stays in the series identity.

Form factor is resolved in this order: an explicit field in the source; a lookup from the instance type maintained in `internal/normalize` (for example `p5.48xlarge` is `sxm`); otherwise `unknown`. Many catalogs name the GPU as just "H100" without the form factor (whether SkyPilot's `AcceleratorName` distinguishes SXM from PCIe is to verify), so a meaningful share of rows will carry `unknown`. They are kept as their own series and never merged into `sxm` or `pcie`.

## 6. GPU identifiers

`gpu_slug` is the canonical identifier for a GPU product. Grammar:

```
<vendor>-<model>[-<form_factor>]-<memory>gb
```

- `vendor`: `nvidia`, `amd`.
- `model`: lowercase, letters and digits only, as the vendor markets it with spaces removed (`h100`, `h200`, `b200`, `a100`, `l40s`, `a10g`, `rtx4090`, `mi300x`).
- `form_factor`: present only when the model ships in more than one module type (`sxm`, `pcie`, `nvl`, or `unknown` when it could not be resolved). Omitted for single-form-factor products.
- `memory`: the vendor's marketed capacity in GB as an integer (`80gb`, `141gb`, `180gb`).

The slug is decomposed into `gpu_vendor`, `gpu_model`, `form_factor` and `gpu_memory_gb` on every observation. The mapping from each source's GPU strings to slugs lives in `internal/normalize`, and changes to it bump this document. Rows whose GPU cannot be mapped to a slug are not emitted as observations; their count is recorded in the run manifest.

Proposed MVP list (9 slugs; proposed, not final):

| gpu_slug | Vendor | Model | Form factor | Memory (GB) |
|---|---|---|---|---|
| `nvidia-h100-sxm-80gb` | nvidia | h100 | sxm | 80 |
| `nvidia-h100-pcie-80gb` | nvidia | h100 | pcie | 80 |
| `nvidia-h200-sxm-141gb` | nvidia | h200 | sxm | 141 |
| `nvidia-b200-sxm-180gb` | nvidia | b200 | sxm | 180 |
| `nvidia-a100-sxm-80gb` | nvidia | a100 | sxm | 80 |
| `nvidia-a100-pcie-40gb` | nvidia | a100 | pcie | 40 |
| `nvidia-l40s-48gb` | nvidia | l40s | pcie | 48 |
| `nvidia-rtx4090-24gb` | nvidia | rtx4090 | pcie | 24 |
| `amd-mi300x-192gb` | amd | mi300x | unknown (`oam` proposed) | 192 |

`nvidia-a10g-24gb` (AWS g5 family) is the first candidate for addition after the MVP.

## 7. Regions

Two fields describe location. `region_raw` is the provider's own region string, verbatim, so nothing is lost. `region_bucket` is a coarse geography for grouping, assigned by a mapping table maintained in `internal/normalize`.

| region_bucket | Covers | Example `region_raw` values |
|---|---|---|
| `us-east` | US East Coast | `us-east-1` (AWS), `eastus` (Azure), `us-east-1` (Lambda) |
| `us-west` | US West Coast | `us-west-2` (AWS), `westus2` (Azure), `us-west-1` (Lambda) |
| `us-central` | US interior | `us-central1` (GCP), `us-south-1` (Lambda) |
| `ca` | Canada | `ca-central-1` |
| `eu-west` | Ireland, France, Netherlands, Belgium | `eu-west-1`, `europe-west1`, `westeurope` |
| `eu-central` | Germany, Switzerland, Poland | `eu-central-1`, `germanywestcentral`, `europe-central-1` (Lambda) |
| `eu-north` | Nordics | `eu-north-1`, `europe-north1` |
| `uk` | United Kingdom | `eu-west-2` (AWS London), `uksouth` |
| `in` | India | `ap-south-1`, `asia-south-1` (Lambda) |
| `sg` | Singapore | `ap-southeast-1`, `asia-southeast1` |
| `jp` | Japan | `ap-northeast-1`, `japaneast` |
| `au` | Australia | `ap-southeast-2`, `australiaeast` |
| `me` | Middle East | `me-central-1`, `uaenorth` |
| `sa` | South America | `sa-east-1`, `brazilsouth` |
| `global` | The listing has no region | `region_raw` is the empty string |
| `unknown` | A region is present but not in the mapping table | anything unmapped |

Rules:

- Many neoclouds publish a single price with no region. Then `region_raw` is empty and `region_bucket` is `global`. This is a real property of the listing, not missing data.
- A region string that is not in the mapping table maps to `unknown`, never to a guess. Unmapped strings seen in a run are listed in the run manifest so the table can be extended.
- A bucket is for grouping, not for claiming capacity. The SkyPilot Lambda fetcher emits one row per region from a hardcoded list regardless of capacity, so those rows carry a region bucket and `availability_signal = none`.
- Finer locations (AWS availability zones, RunPod datacenters) are kept in `zone_raw` (proposed) and are part of the series identity when present.
- Country-only strings (for example a marketplace host reporting only `US`) cannot be placed in a sub-national bucket. Adding `us` and `eu` fallback buckets is proposed; until then such strings map to `unknown`.

## 8. Normalization rules

1. **Per GPU-hour.** All prices are expressed in USD per GPU-hour. The original number, basis and currency are retained alongside.
2. **USD.** Prices not published in USD are converted at a daily reference rate (ECB reference rates proposed); `currency_original` and `fx_rate_to_usd` record the inputs. All MVP sources publish in USD, so no conversion occurs in 0.1.0.
3. **Bundled resources.** Bundled storage, egress or networking is stripped only when the provider itemizes it. Otherwise the price is kept as published and `bundled = true`.
4. **Minimum commitment.** Recorded in `min_commitment_hours`, 0 when none. For `reserved` prices this is the term length.
5. **No averaging across form factors.** `sxm`, `pcie`, `nvl` and `unknown` are separate series.
6. **No averaging across price types.** A rate card and a spot price for the same instance are separate series and are never combined into one index.
7. **No mixing executed trades with listings.** If an executed-trade source is ever added, it is published as its own series and never enters a listings index.
8. **No blending of sources.** Rollups report one source per series. Proposed preference when several sources report the same series: the provider's own API, then SkyPilot, then gpuhunt (proposed; to settle before rollups ship).
9. **Marketplaces.** For marketplace sources the collector groups live offers by (provider, gpu_slug, region_raw, node_gpu_count, tier), keeps offers the marketplace marks as currently rentable, and emits one observation per statistic in `price_stat`: `min` and `p50` in 0.1.0 (proposed). `offer_count` is the size of the group. Every individual offer remains in the raw snapshot.
10. **No rounding, no deduplication.** Prices are stored as published. Raw snapshots are written every run even when the upstream body is unchanged; the manifest records a content hash so readers can deduplicate.
11. **Tier.** The provider's own name for the purchase option (`on_demand`, `spot`, `interruptible`, and so on) is kept in `tier` (proposed) so that, for example, GCP on-demand and GCP fixed-discount spot, both `rate_card`, stay separate series.

## 9. Staleness and carry-forward

- An observation is written every collection cycle for every (source, provider, gpu_slug, region, price_type, tier) series the source reports in that cycle. The collection cycle is hourly (proposed).
- If an upstream fetch fails, or a series disappears from an upstream, no row is written. gputick never fabricates observations. The run manifest records the failure.
- Observation files therefore always have `stale = false` and `stale_since = null`.
- Downstream rollups and API responses may carry the last observed value forward for at most 72 hours, marking it `stale = true` with `stale_since` set to the `observed_at` of the last real observation. After 72 hours the series shows a gap.
- Raw snapshots are immutable and retained forever. Observations may be re-derived from raw whenever this document changes; each observation carries the `methodology_version` that produced it.

## 10. Sources

| Source | License | Fetch | Auth | price_type | availability_signal | Upstream cadence | Known caveats |
|---|---|---|---|---|---|---|---|
| `skypilot` (SkyPilot catalog, repo `skypilot-org/skypilot-catalog`) | Apache-2.0 | raw CSV over GitHub, `catalogs/v<N>/<cloud>/vms.csv`; columns include `InstanceType`, `AcceleratorName`, `AcceleratorCount`, `vCPUs`, `MemoryGiB`, `GpuInfo`, `Price`, `SpotPrice`, `Region`, `AvailabilityZone` | none | `rate_card`; `dynamic_spot` from `SpotPrice` for AWS and Azure; GCP `SpotPrice` is `rate_card` | `none` | bot-updated several times a day | Lambda rows are one per hardcoded region regardless of capacity: a rate card wearing a region label. `SpotPrice` is mostly empty for neoclouds. Schema bumped seven times in four years: pin `v<N>` and detect header changes. Covers AWS, Azure, GCP and about 20 neoclouds (Lambda 17 regions, RunPod 48 zones, Nebius, Hyperstack, OVH, Scaleway, Seeweb, Verda, Shadeform). |
| `gpuhunt` (dstack) | MPL-2.0 | public S3 bucket `dstack-gpu-pricing` (eu-west-1), key `v3/<provider>/version`, then a zip containing one CSV per provider; 14 columns including `provider`, `instance_name`, `location`, `price`, `cpu`, `memory`, `gpu_name`, `gpu_count`, `gpu_memory`, `spot`, `disk_size` (remaining columns to verify) | none | `rate_card`; `dynamic_spot` where `spot` is set for AWS and Azure | `none` | several updates a day | Nine "online" providers (Crusoe, Vast, Vultr and others) are not in the bucket. |
| `aws` | provider API terms (to verify) | EC2 spot price history (90 days per call) and spot placement score APIs; on-demand from the pricing API or the SkyPilot catalog | IAM credentials, free | `dynamic_spot`, `rate_card` | `placement_score` | gputick cycle | Spot prices are per availability zone. The placement score is a prediction from 1 to 10 requested per region (per-zone granularity to verify). |
| `azure-retail` | provider API terms (to verify) | Azure retail prices API | none | `rate_card`, `dynamic_spot` | `none` | gputick cycle | The API has no availability signal; `available` is null. Spot discounts of 70 to 80 percent coexist with frequently unobtainable capacity. |
| `lambda` | provider API terms (to verify) | instance-types API, `regions_with_capacity_available` per instance type | free API key | `rate_card` | `capacity_flag` | gputick cycle | One global price per instance type. `false` rows are emitted against a Lambda region list maintained in `internal/normalize`, not reported by the API. |
| `runpod` | provider API terms (to verify) | keyless GraphQL API: per GPU type stock status and minimum spot bid, plus on-demand for secure and community cloud | none | `rate_card` (`runpod-secure`), `marketplace_ask` (`runpod-community`), `min_bid` (spot) | `stock_level` | gputick cycle | Secure and community cloud are modeled as different providers. Exact stock status vocabulary to verify. |
| `vast` | provider API terms (to verify) | marketplace search API: live offers with per-machine asks and an offer count | to verify | `marketplace_ask`; `min_bid` for interruptible (to verify) | `offer_count` | gputick cycle | Locations are host-reported and coarse (string format to verify). Offer grouping and filters are in section 8, rule 9. |
| `tensordock` | provider API terms (to verify) | marketplace API: per-host asks and availability | to verify | `marketplace_ask` | `offer_count` | gputick cycle | Endpoint details to verify before implementation. |
| `coreweave-web`, `crusoe-web` (planned) | public pricing pages | HTML or JSON pricing page fetch, no headless browsers | none | `rate_card` | `none` | gputick cycle | Fragile by nature. A page change produces a failed run, never a guessed price. |
| executed trades | n/a | none | n/a | `executed_trade` | n/a | n/a | gputick has no executed-trade source. SF Compute is the obvious candidate if a public feed becomes available. |

## 11. What we do not claim

- We do not measure executed prices for any source today. Every number is a listing: a rate card, a provider-set spot price, a marketplace ask or a bid floor.
- A rate card with a region label does not mean capacity exists in that region. Only rows with an availability signal other than `none` say anything about obtainability, and then only what the provider chose to publish.
- Availability signals are provider-reported and coarse. We do not verify them by launching instances, and we never will.
- Placement scores are predictions made by AWS about a hypothetical request, not observations of free capacity.
- We cannot see private, negotiated or enterprise pricing. Large buyers pay prices that never appear in any source we read.
- "Effective price", the listed price weighted by the probability the machine is actually obtainable, does not exist in any public dataset, including this one. If gputick ever publishes one it is a derived estimate, and its formula and inputs will be published in this document before it is shown anywhere.
- Mirrored catalogs inherit their upstream's errors and delays. gputick records what the catalog said at `observed_at`, which may lag the provider's own page.
- gputick says nothing about performance, reliability, driver versions, network quality or support. A cheaper GPU-hour is not a better one.

## 12. Versioning and re-derivation

This document carries a semantic version, starting at 0.1.0 in pre-alpha. Any change to the price type or availability taxonomies, the slug grammar or mapping tables, the region buckets, the normalization rules or the staleness policy bumps the version, is recorded in the changelog below, and triggers a re-derivation of all observations from raw snapshots. Re-derived observations carry the new `methodology_version`. Editorial changes that do not alter any derived value bump the patch component and do not trigger re-derivation. Compatibility rules for the file formats are in [docs/SCHEMA.md](SCHEMA.md), section 8.

## 13. How to cite

Data are published under CC-BY-4.0. Mirrored snapshots retain their upstream attribution: SkyPilot catalog (Apache-2.0) and dstack gpuhunt (MPL-2.0). Code is Apache-2.0 (see [LICENSE](../LICENSE)).

Suggested citation:

> gputick contributors. gputick: an open time series of GPU rental prices and availability. Methodology version 0.1.0. https://github.com/HMetcalfeW/gputick. Accessed YYYY-MM-DD.

A DOI through Zenodo is planned for the first tagged data release; this section will be updated with it.

## Changelog

| Version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-10-24 | Initial draft. Defines price types, availability signals, form factor and node normalization, slug grammar with a proposed 9-slug MVP list, region buckets, normalization rules, the 72-hour carry-forward policy and the source table. No data collected yet. |
