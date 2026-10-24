package schema_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/pkg/schema"
)

func TestObservation_Validate_ExampleRowsPass(t *testing.T) {
	d := rowD(t)
	require.NoError(t, d.Validate())

	b := rowD(t)
	b.Source, b.Provider = "aws", "aws"
	b.InstanceType = ptr("p5.48xlarge")
	b.Interconnect = schema.InterconnectEthernet
	b.ZoneRaw = ptr("us-east-1a")
	b.PriceType, b.Tier = schema.PriceTypeDynamicSpot, "spot"
	b.PriceOriginal, b.PriceUSDPerGPUHour = 33.28, 4.16
	b.Availability = schema.Availability{Signal: schema.SignalPlacementScore, Value: ptr("7")}
	require.NoError(t, b.Validate())

	c := rowD(t)
	c.Source, c.Provider = "vast", "vast"
	c.GPUSlug, c.GPUModel, c.FormFactor, c.GPUMemoryGB = "nvidia-rtx4090-24gb", "rtx4090", schema.FormFactorPCIe, 24
	c.NodeGPUCount, c.InstanceType = 1, nil
	c.RegionRaw, c.RegionBucket = "Texas, US", schema.RegionUSCentral
	c.PriceType, c.PriceStat = schema.PriceTypeMarketplaceAsk, schema.PriceStatMin
	c.PriceBasis, c.PriceOriginal, c.PriceUSDPerGPUHour = schema.PriceBasisPerGPUHour, 0.34, 0.34
	c.Availability = schema.Availability{Signal: schema.SignalOfferCount, Value: ptr("212"), Available: ptr(true)}
	require.NoError(t, c.Validate())
}

func TestObservation_Validate_RejectsViolations(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(o *schema.Observation)
		wantErr string
	}{
		{"zero observed_at", func(o *schema.Observation) { o.ObservedAt = time.Time{} }, "observed_at is zero"},
		{"empty run_id", func(o *schema.Observation) { o.RunID = "" }, "run_id is empty"},
		{"empty tier", func(o *schema.Observation) { o.Tier = "" }, "tier is empty"},
		{"bad form_factor", func(o *schema.Observation) { o.FormFactor = "SXM" }, `form_factor "SXM"`},
		{"bad interconnect", func(o *schema.Observation) { o.Interconnect = "" }, `interconnect ""`},
		{"bad region_bucket", func(o *schema.Observation) { o.RegionBucket = "us" }, `region_bucket "us"`},
		{"bad price_type", func(o *schema.Observation) { o.PriceType = "spot" }, `price_type "spot"`},
		{"bad price_basis", func(o *schema.Observation) { o.PriceBasis = "hourly" }, `price_basis "hourly"`},
		{"bad price_stat", func(o *schema.Observation) { o.PriceStat = "avg" }, `price_stat "avg"`},
		{"zero memory", func(o *schema.Observation) { o.GPUMemoryGB = 0 }, "gpu_memory_gb must be positive"},
		{"zero node count", func(o *schema.Observation) { o.NodeGPUCount = 0 }, "node_gpu_count must be positive"},
		{"zero price", func(o *schema.Observation) { o.PriceUSDPerGPUHour = 0 }, "price_usd_per_gpu_hour must be positive"},
		{"negative original", func(o *schema.Observation) { o.PriceOriginal = -1 }, "price_original must be positive"},
		{"negative commitment", func(o *schema.Observation) { o.MinCommitmentHours = -1 }, "min_commitment_hours must not be negative"},
		{"fx rate with USD", func(o *schema.Observation) { o.FXRateToUSD = ptr(1.0) }, "fx_rate_to_usd must be null"},
		{"stale without since", func(o *schema.Observation) { o.Stale = true }, "stale_since is required"},
		{"since without stale", func(o *schema.Observation) { o.StaleSince = ptr(o.ObservedAt) }, "stale_since must be null"},
		{"available contradicts value", func(o *schema.Observation) { o.Available = ptr(true) }, "does not match derivation"},
		{"value for signal none", func(o *schema.Observation) {
			o.Availability = schema.Availability{Signal: schema.SignalNone, Value: ptr("1")}
		}, "value must be null for signal none"},
		{"inferred signal", func(o *schema.Observation) {
			o.Availability = schema.Availability{Signal: schema.SignalInferred}
		}, "inferred availability signals are not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := rowD(t)
			tt.mutate(&o)
			err := o.Validate()
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestObservation_Validate_ReportsEveryViolation(t *testing.T) {
	o := rowD(t)
	o.RunID = ""
	o.PriceType = "spot"
	o.Stale = true
	err := o.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "run_id is empty")
	require.ErrorContains(t, err, `price_type "spot"`)
	assert.ErrorContains(t, err, "stale_since is required")
}

func TestAvailability_DeriveAvailable(t *testing.T) {
	tests := []struct {
		name    string
		in      schema.Availability
		want    *bool
		wantErr error
	}{
		{"none", schema.Availability{Signal: schema.SignalNone}, nil, nil},
		{"none with value", schema.Availability{Signal: schema.SignalNone, Value: ptr("x")}, nil, schema.ErrAvailabilityValue},
		{"capacity true", schema.Availability{Signal: schema.SignalCapacityFlag, Value: ptr("true")}, ptr(true), nil},
		{"capacity false", schema.Availability{Signal: schema.SignalCapacityFlag, Value: ptr("false")}, ptr(false), nil},
		{"capacity yes", schema.Availability{Signal: schema.SignalCapacityFlag, Value: ptr("yes")}, nil, schema.ErrAvailabilityValue},
		{"capacity null", schema.Availability{Signal: schema.SignalCapacityFlag}, nil, schema.ErrAvailabilityValue},
		{"stock high", schema.Availability{Signal: schema.SignalStockLevel, Value: ptr("high")}, ptr(true), nil},
		{"stock none", schema.Availability{Signal: schema.SignalStockLevel, Value: ptr("none")}, ptr(false), nil},
		{"stock empty", schema.Availability{Signal: schema.SignalStockLevel, Value: ptr("")}, nil, schema.ErrAvailabilityValue},
		{"offers 212", schema.Availability{Signal: schema.SignalOfferCount, Value: ptr("212")}, ptr(true), nil},
		{"offers 0", schema.Availability{Signal: schema.SignalOfferCount, Value: ptr("0")}, ptr(false), nil},
		{"offers negative", schema.Availability{Signal: schema.SignalOfferCount, Value: ptr("-1")}, nil, schema.ErrAvailabilityValue},
		{"offers separators", schema.Availability{Signal: schema.SignalOfferCount, Value: ptr("1,212")}, nil, schema.ErrAvailabilityValue},
		{"score 7", schema.Availability{Signal: schema.SignalPlacementScore, Value: ptr("7")}, nil, nil},
		{"score 0", schema.Availability{Signal: schema.SignalPlacementScore, Value: ptr("0")}, nil, schema.ErrAvailabilityValue},
		{"score 11", schema.Availability{Signal: schema.SignalPlacementScore, Value: ptr("11")}, nil, schema.ErrAvailabilityValue},
		{"inferred", schema.Availability{Signal: schema.SignalInferred}, nil, schema.ErrInferredUndefined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.DeriveAvailable()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAvailability_Validate_UnknownSignal(t *testing.T) {
	err := schema.Availability{Signal: "stock"}.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, `availability_signal "stock"`)
}

func manifest(t *testing.T) schema.RunManifest {
	t.Helper()
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	return schema.RunManifest{
		RunID:              "20261024T120000Z-3f9a1c2e",
		StartedAt:          start,
		FinishedAt:         start.Add(42 * time.Second),
		CollectorVersion:   "gputick dev",
		MethodologyVersion: schema.MethodologyVersion,
		Status:             schema.RunStatusPartial,
		ObservationsPath:   ptr("obs/date=2026-10-24/20261024T120000Z-3f9a1c2e.parquet"),
		ObservationsTotal:  1200,
		Sources: []schema.SourceRun{
			{
				Source: "skypilot", Status: schema.SourceRunOK, StartedAt: start, FinishedAt: start.Add(9 * time.Second),
				DurationMillis: 9000, UpstreamVersion: ptr("v7/a1b2c3d"), UpstreamSHA256: ptr("deadbeef"),
				RawPath: ptr("raw/source=skypilot/date=2026-10-24/hour=12/20261024T120000Z-3f9a1c2e.parquet"),
				RowsRaw: 5000, RowsObservations: 1200, RowsUnmappedGPU: 12, UnmappedRegions: []string{"mars-1"},
			},
			{
				Source: "lambda", Status: schema.SourceRunFailed, StartedAt: start, FinishedAt: start.Add(time.Minute),
				DurationMillis: 60000, UnmappedRegions: []string{}, Error: ptr("context deadline exceeded"),
			},
			{
				Source: "vast", Status: schema.SourceRunSkipped, StartedAt: start, FinishedAt: start,
				UnmappedRegions: []string{},
			},
		},
	}
}

func TestRunManifest_ValidateAndJSON(t *testing.T) {
	m := manifest(t)
	require.NoError(t, m.Validate())

	data, err := json.Marshal(m)
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &keys))
	assert.Len(t, keys, 9)
	assert.Contains(t, string(data), `"unmapped_regions":[]`, "empty slices serialize as arrays, not null")
	assert.Contains(t, string(data), `"duration_ms":9000`)
}

func TestRunManifest_Validate_RejectsViolations(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m *schema.RunManifest)
		wantErr string
	}{
		{"empty run_id", func(m *schema.RunManifest) { m.RunID = "" }, "run_id is empty"},
		{"finished before started", func(m *schema.RunManifest) { m.FinishedAt = m.StartedAt.Add(-time.Second) }, "finished_at is before started_at"},
		{"bad status", func(m *schema.RunManifest) { m.Status = "timeout" }, `status "timeout"`},
		{"failed with path", func(m *schema.RunManifest) { m.Status = schema.RunStatusFailed }, "observations_path must be null"},
		{"ok without path", func(m *schema.RunManifest) { m.ObservationsPath = nil }, "observations_path is required"},
		{"nil sources", func(m *schema.RunManifest) { m.Sources = nil }, "sources must be an array"},
		{"source bad status", func(m *schema.RunManifest) { m.Sources[0].Status = "timeout" }, `sources[0]: status "timeout"`},
		{"failed source without error", func(m *schema.RunManifest) { m.Sources[1].Error = nil }, "error is required when status is failed"},
		{"ok source with error", func(m *schema.RunManifest) { m.Sources[0].Error = ptr("boom") }, "error must be null unless status is failed"},
		{"failed source with raw_path", func(m *schema.RunManifest) { m.Sources[1].RawPath = ptr("raw/x") }, "raw_path must be null when status is failed"},
		{"nil unmapped_regions", func(m *schema.RunManifest) { m.Sources[2].UnmappedRegions = nil }, "unmapped_regions must be an array"},
		{"empty source slug", func(m *schema.RunManifest) { m.Sources[2].Source = "" }, "sources[2]: source is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := manifest(t)
			tt.mutate(&m)
			err := m.Validate()
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
