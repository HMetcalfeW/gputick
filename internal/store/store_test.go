package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/internal/source"
	"github.com/HMetcalfeW/gputick/internal/store"
	"github.com/HMetcalfeW/gputick/pkg/schema"
)

const runID = "20261024T120000Z-3f9a1c2e"

func ptr[T any](v T) *T { return &v }

func TestPaths(t *testing.T) {
	raw, err := store.RawPath("skypilot", runID)
	require.NoError(t, err)
	assert.Equal(t, "raw/source=skypilot/date=2026-10-24/hour=12/20261024T120000Z-3f9a1c2e.parquet", raw)

	obs, err := store.ObservationsPath(runID)
	require.NoError(t, err)
	assert.Equal(t, "obs/date=2026-10-24/20261024T120000Z-3f9a1c2e.parquet", obs)

	man, err := store.ManifestPath(runID)
	require.NoError(t, err)
	assert.Equal(t, "manifest/date=2026-10-24/20261024T120000Z-3f9a1c2e.json", man)
}

func TestPaths_HourPartitionIsZeroPadded(t *testing.T) {
	raw, err := store.RawPath("azure-retail", "20261024T030000Z-00000000")
	require.NoError(t, err)
	assert.Equal(t, "raw/source=azure-retail/date=2026-10-24/hour=03/20261024T030000Z-00000000.parquet", raw)
}

func TestRawPath_RejectsBadSourceName(t *testing.T) {
	for _, name := range []string{"", "SkyPilot", "sky pilot", "sky_pilot", "-sky", "sky/pilot"} {
		t.Run(name, func(t *testing.T) {
			_, err := store.RawPath(name, runID)
			assert.ErrorContains(t, err, "lowercase ASCII")
		})
	}
}

func TestPaths_RejectBadRunID(t *testing.T) {
	for _, id := range []string{"", "run-1", "20261024T120000Z-3F9A1C2E"} {
		t.Run(id, func(t *testing.T) {
			_, err := store.RawPath("skypilot", id)
			require.Error(t, err)
			_, err = store.ObservationsPath(id)
			require.Error(t, err)
			_, err = store.ManifestPath(id)
			assert.Error(t, err)
		})
	}
}

func observation() schema.Observation {
	return schema.Observation{
		ObservedAt: time.Date(2026, 10, 24, 12, 0, 7, 0, time.UTC), RunID: runID, Source: "lambda", Provider: "lambda",
		GPUSlug: "nvidia-h100-sxm-80gb", GPUVendor: "nvidia", GPUModel: "h100",
		FormFactor: schema.FormFactorSXM, GPUMemoryGB: 80, Interconnect: schema.InterconnectUnknown,
		NodeGPUCount: 8, RegionRaw: "us-east-1", RegionBucket: schema.RegionUSEast,
		PriceType: schema.PriceTypeRateCard, Tier: "on_demand", PriceStat: schema.PriceStatList,
		PriceUSDPerGPUHour: 2.99, PriceBasis: schema.PriceBasisPerNodeHour, PriceOriginal: 23.92,
		CurrencyOriginal: "USD", AvailabilitySignal: schema.SignalNone, MethodologyVersion: schema.MethodologyVersion,
	}
}

func TestValidateObservations(t *testing.T) {
	require.NoError(t, store.ValidateObservations(runID, nil))
	require.NoError(t, store.ValidateObservations(runID, []schema.Observation{observation(), observation()}))

	tests := []struct {
		name    string
		mutate  func(o *schema.Observation)
		wantErr string
	}{
		{"invalid", func(o *schema.Observation) { o.Provider = "" }, "observation 1: provider is empty"},
		{"foreign run", func(o *schema.Observation) { o.RunID = "20261024T110000Z-00000000" }, "does not belong to run"},
		{"stale", func(o *schema.Observation) { o.Stale, o.StaleSince = true, ptr(o.ObservedAt) }, "stale rows belong in rollups"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := observation()
			tt.mutate(&o)
			err := store.ValidateObservations(runID, []schema.Observation{observation(), o})
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestNewRawMetadata(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 1, 0, time.FixedZone("EDT", -4*3600))
	raw := source.RawResult{Parts: []source.Part{
		{Ref: "a", UpstreamVersion: "v1", FetchedAt: at, Bytes: 3, SHA256: "aa", Columns: []string{"source", "x"}, Rows: []source.RawRow{{"x": "1"}}},
		{Ref: "b", FetchedAt: at, SHA256: "bb"},
	}}
	m := store.NewRawMetadata(runID, "gpuhunt", raw, []string{"payload_source", "x"})
	assert.Equal(t, store.RawMetadata{
		Source: "gpuhunt", RunID: runID,
		Columns: []store.RawColumn{{Upstream: "source", Column: "payload_source"}, {Upstream: "x", Column: "x"}},
		Parts: []store.RawPart{
			{Ref: "a", UpstreamVersion: "v1", FetchedAt: at.UTC(), Bytes: 3, SHA256: "aa", Columns: []string{"source", "x"}, Rows: 1},
			{Ref: "b", FetchedAt: at.UTC(), SHA256: "bb", Columns: []string{}, Rows: 0},
		},
	}, m)
	assert.Equal(t, time.UTC, m.Parts[0].FetchedAt.Location(), "metadata times are UTC")
}
