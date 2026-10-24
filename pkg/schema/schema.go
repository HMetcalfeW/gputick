// Package schema holds the exported data contract for gputick: the Observation
// row, the run manifest, source metadata and the enumerations they use. It is
// the only package in the module intended for import by other programs, so it
// depends on the standard library alone. docs/SCHEMA.md is the human-readable
// form of this contract and docs/METHODOLOGY.md defines what the values mean.
package schema

import "time"

// MethodologyVersion is the docs/METHODOLOGY.md version this package implements.
const MethodologyVersion = "0.1.0"

type (
	PriceType          string
	AvailabilitySignal string
	FormFactor         string
	Interconnect       string
	PriceBasis         string
	PriceStat          string
	RegionBucket       string
	RunStatus          string
	SourceRunStatus    string
)

const (
	PriceTypeRateCard       PriceType = "rate_card"
	PriceTypeDynamicSpot    PriceType = "dynamic_spot"
	PriceTypeMarketplaceAsk PriceType = "marketplace_ask"
	PriceTypeMinBid         PriceType = "min_bid"
	PriceTypeExecutedTrade  PriceType = "executed_trade"
	PriceTypeReserved       PriceType = "reserved"

	SignalNone           AvailabilitySignal = "none"
	SignalCapacityFlag   AvailabilitySignal = "capacity_flag"
	SignalStockLevel     AvailabilitySignal = "stock_level"
	SignalOfferCount     AvailabilitySignal = "offer_count"
	SignalPlacementScore AvailabilitySignal = "placement_score"
	SignalInferred       AvailabilitySignal = "inferred"

	FormFactorSXM     FormFactor = "sxm"
	FormFactorPCIe    FormFactor = "pcie"
	FormFactorNVL     FormFactor = "nvl"
	FormFactorUnknown FormFactor = "unknown"

	InterconnectNVLink     Interconnect = "nvlink"
	InterconnectInfiniBand Interconnect = "infiniband"
	InterconnectEthernet   Interconnect = "ethernet"
	InterconnectUnknown    Interconnect = "unknown"

	PriceBasisPerGPUHour  PriceBasis = "per_gpu_hour"
	PriceBasisPerNodeHour PriceBasis = "per_node_hour"

	PriceStatList PriceStat = "list"
	PriceStatMin  PriceStat = "min"
	PriceStatP50  PriceStat = "p50"

	RegionUSEast    RegionBucket = "us-east"
	RegionUSWest    RegionBucket = "us-west"
	RegionUSCentral RegionBucket = "us-central"
	RegionCA        RegionBucket = "ca"
	RegionEUWest    RegionBucket = "eu-west"
	RegionEUCentral RegionBucket = "eu-central"
	RegionEUNorth   RegionBucket = "eu-north"
	RegionUK        RegionBucket = "uk"
	RegionIN        RegionBucket = "in"
	RegionSG        RegionBucket = "sg"
	RegionJP        RegionBucket = "jp"
	RegionAU        RegionBucket = "au"
	RegionME        RegionBucket = "me"
	RegionSA        RegionBucket = "sa"
	RegionGlobal    RegionBucket = "global"
	RegionUnknown   RegionBucket = "unknown"

	RunStatusOK      RunStatus = "ok"
	RunStatusPartial RunStatus = "partial"
	RunStatusFailed  RunStatus = "failed"

	SourceRunOK      SourceRunStatus = "ok"
	SourceRunFailed  SourceRunStatus = "failed"
	SourceRunSkipped SourceRunStatus = "skipped"
)

var (
	priceTypes = setOf(PriceTypeRateCard, PriceTypeDynamicSpot, PriceTypeMarketplaceAsk,
		PriceTypeMinBid, PriceTypeExecutedTrade, PriceTypeReserved)
	signals = setOf(SignalNone, SignalCapacityFlag, SignalStockLevel, SignalOfferCount,
		SignalPlacementScore, SignalInferred)
	formFactors   = setOf(FormFactorSXM, FormFactorPCIe, FormFactorNVL, FormFactorUnknown)
	interconnects = setOf(InterconnectNVLink, InterconnectInfiniBand, InterconnectEthernet, InterconnectUnknown)
	priceBases    = setOf(PriceBasisPerGPUHour, PriceBasisPerNodeHour)
	priceStats    = setOf(PriceStatList, PriceStatMin, PriceStatP50)
	regionBuckets = setOf(RegionUSEast, RegionUSWest, RegionUSCentral, RegionCA, RegionEUWest,
		RegionEUCentral, RegionEUNorth, RegionUK, RegionIN, RegionSG, RegionJP, RegionAU,
		RegionME, RegionSA, RegionGlobal, RegionUnknown)
	runStatuses       = setOf(RunStatusOK, RunStatusPartial, RunStatusFailed)
	sourceRunStatuses = setOf(SourceRunOK, SourceRunFailed, SourceRunSkipped)
)

func setOf[T comparable](vs ...T) map[T]struct{} {
	m := make(map[T]struct{}, len(vs))
	for _, v := range vs {
		m[v] = struct{}{}
	}
	return m
}

func (v PriceType) Valid() bool          { _, ok := priceTypes[v]; return ok }
func (v AvailabilitySignal) Valid() bool { _, ok := signals[v]; return ok }
func (v FormFactor) Valid() bool         { _, ok := formFactors[v]; return ok }
func (v Interconnect) Valid() bool       { _, ok := interconnects[v]; return ok }
func (v PriceBasis) Valid() bool         { _, ok := priceBases[v]; return ok }
func (v PriceStat) Valid() bool          { _, ok := priceStats[v]; return ok }
func (v RegionBucket) Valid() bool       { _, ok := regionBuckets[v]; return ok }
func (v RunStatus) Valid() bool          { _, ok := runStatuses[v]; return ok }
func (v SourceRunStatus) Valid() bool    { _, ok := sourceRunStatuses[v]; return ok }

// Availability is the provider-reported signal attached to an observation.
// It is embedded in Observation and flattened to top-level columns by both
// encoding/json and parquet-go. Value is encoded per signal; see
// docs/SCHEMA.md section 3 and DeriveAvailable.
type Availability struct {
	Signal    AvailabilitySignal `parquet:"availability_signal" json:"availability_signal"`
	Value     *string            `parquet:"availability_value"  json:"availability_value"`
	Available *bool              `parquet:"available"           json:"available"`
}

// Observation is one row of the gputick dataset: what one source reported
// about one series in one collection run. Field order matches docs/SCHEMA.md
// so JSON output reads in the documented order.
type Observation struct {
	ObservedAt         time.Time    `parquet:"observed_at,timestamp(microsecond)" json:"observed_at"`
	RunID              string       `parquet:"run_id"                             json:"run_id"`
	Source             string       `parquet:"source"                             json:"source"`
	Provider           string       `parquet:"provider"                           json:"provider"`
	GPUSlug            string       `parquet:"gpu_slug"                           json:"gpu_slug"`
	GPUVendor          string       `parquet:"gpu_vendor"                         json:"gpu_vendor"`
	GPUModel           string       `parquet:"gpu_model"                          json:"gpu_model"`
	FormFactor         FormFactor   `parquet:"form_factor"                        json:"form_factor"`
	GPUMemoryGB        int32        `parquet:"gpu_memory_gb"                      json:"gpu_memory_gb"`
	Interconnect       Interconnect `parquet:"interconnect"                       json:"interconnect"`
	NodeGPUCount       int32        `parquet:"node_gpu_count"                     json:"node_gpu_count"`
	InstanceType       *string      `parquet:"instance_type"                      json:"instance_type"`
	RegionRaw          string       `parquet:"region_raw"                         json:"region_raw"`
	ZoneRaw            *string      `parquet:"zone_raw"                           json:"zone_raw"`
	RegionBucket       RegionBucket `parquet:"region_bucket"                      json:"region_bucket"`
	PriceType          PriceType    `parquet:"price_type"                         json:"price_type"`
	Tier               string       `parquet:"tier"                               json:"tier"`
	PriceStat          PriceStat    `parquet:"price_stat"                         json:"price_stat"`
	PriceUSDPerGPUHour float64      `parquet:"price_usd_per_gpu_hour"             json:"price_usd_per_gpu_hour"`
	PriceBasis         PriceBasis   `parquet:"price_basis"                        json:"price_basis"`
	PriceOriginal      float64      `parquet:"price_original"                     json:"price_original"`
	CurrencyOriginal   string       `parquet:"currency_original"                  json:"currency_original"`
	FXRateToUSD        *float64     `parquet:"fx_rate_to_usd"                     json:"fx_rate_to_usd"`
	MinCommitmentHours int32        `parquet:"min_commitment_hours"               json:"min_commitment_hours"`
	Bundled            bool         `parquet:"bundled"                            json:"bundled"`
	Availability
	Stale              bool       `parquet:"stale"                               json:"stale"`
	StaleSince         *time.Time `parquet:"stale_since,timestamp(microsecond)"  json:"stale_since"`
	MethodologyVersion string     `parquet:"methodology_version"                 json:"methodology_version"`
	UpstreamVersion    *string    `parquet:"upstream_version"                    json:"upstream_version"`
}

// Source describes one feed gputick reads. It backs the source table in
// docs/METHODOLOGY.md section 10 and the source registry in the collector.
type Source struct {
	Slug               string             `json:"slug"`
	Name               string             `json:"name"`
	License            string             `json:"license"`
	Fetch              string             `json:"fetch"`
	Auth               string             `json:"auth"`
	PriceTypes         []PriceType        `json:"price_types"`
	AvailabilitySignal AvailabilitySignal `json:"availability_signal"`
	Cadence            string             `json:"cadence"`
}
