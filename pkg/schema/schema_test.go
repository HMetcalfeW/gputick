package schema_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/pkg/schema"
)

// rowD is example row D from docs/SCHEMA.md section 6: a Lambda capacity flag.
func rowD(t *testing.T) schema.Observation {
	t.Helper()
	return schema.Observation{
		ObservedAt:         time.Date(2026, 10, 24, 12, 0, 4, 0, time.UTC),
		RunID:              "20261024T120000Z-3f9a1c2e",
		Source:             "lambda",
		Provider:           "lambda",
		GPUSlug:            "nvidia-h100-sxm-80gb",
		GPUVendor:          "nvidia",
		GPUModel:           "h100",
		FormFactor:         schema.FormFactorSXM,
		GPUMemoryGB:        80,
		Interconnect:       schema.InterconnectUnknown,
		NodeGPUCount:       8,
		InstanceType:       ptr("gpu_8x_h100_sxm5"),
		RegionRaw:          "us-east-1",
		RegionBucket:       schema.RegionUSEast,
		PriceType:          schema.PriceTypeRateCard,
		Tier:               "on_demand",
		PriceStat:          schema.PriceStatList,
		PriceUSDPerGPUHour: 2.99,
		PriceBasis:         schema.PriceBasisPerNodeHour,
		PriceOriginal:      23.92,
		CurrencyOriginal:   "USD",
		MinCommitmentHours: 0,
		Bundled:            false,
		Availability: schema.Availability{
			Signal:    schema.SignalCapacityFlag,
			Value:     ptr("false"),
			Available: ptr(false),
		},
		MethodologyVersion: schema.MethodologyVersion,
	}
}

func ptr[T any](v T) *T { return &v }

// Verbatim from docs/SCHEMA.md section 7.
const rowDJSON = `{
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
}`

func TestObservation_JSONMatchesSchemaDoc(t *testing.T) {
	got, err := json.Marshal(rowD(t))
	require.NoError(t, err)
	assert.JSONEq(t, rowDJSON, string(got))

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got, &keys))
	assert.Len(t, keys, 32, "every documented column is present as a key, nulls included")
}

func TestObservation_JSONRoundTrip(t *testing.T) {
	want := rowD(t)
	want.ZoneRaw = ptr("us-east-1a")
	want.Stale = true
	want.StaleSince = ptr(time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))

	data, err := json.Marshal(want)
	require.NoError(t, err)
	var got schema.Observation
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, want, got)
}

func TestEnums_ValuesPinnedToSchemaDoc(t *testing.T) {
	t.Run("price_type", func(t *testing.T) {
		want := []string{"rate_card", "dynamic_spot", "marketplace_ask", "min_bid", "executed_trade", "reserved"}
		got := []schema.PriceType{schema.PriceTypeRateCard, schema.PriceTypeDynamicSpot,
			schema.PriceTypeMarketplaceAsk, schema.PriceTypeMinBid, schema.PriceTypeExecutedTrade,
			schema.PriceTypeReserved}
		for i, g := range got {
			assert.Equal(t, want[i], string(g))
			assert.True(t, g.Valid())
		}
		assert.False(t, schema.PriceType("spot").Valid())
	})
	t.Run("availability_signal", func(t *testing.T) {
		want := []string{"none", "capacity_flag", "stock_level", "offer_count", "placement_score", "inferred"}
		got := []schema.AvailabilitySignal{schema.SignalNone, schema.SignalCapacityFlag,
			schema.SignalStockLevel, schema.SignalOfferCount, schema.SignalPlacementScore, schema.SignalInferred}
		for i, g := range got {
			assert.Equal(t, want[i], string(g))
			assert.True(t, g.Valid())
		}
		assert.False(t, schema.AvailabilitySignal("").Valid())
	})
	t.Run("form_factor", func(t *testing.T) {
		assert.Equal(t, "sxm", string(schema.FormFactorSXM))
		assert.Equal(t, "pcie", string(schema.FormFactorPCIe))
		assert.Equal(t, "nvl", string(schema.FormFactorNVL))
		assert.Equal(t, "unknown", string(schema.FormFactorUnknown))
		assert.False(t, schema.FormFactor("oam").Valid(), "oam is proposed, not adopted, in 0.1.0")
	})
	t.Run("interconnect", func(t *testing.T) {
		for _, v := range []schema.Interconnect{schema.InterconnectNVLink, schema.InterconnectInfiniBand,
			schema.InterconnectEthernet, schema.InterconnectUnknown} {
			assert.True(t, v.Valid(), v)
		}
		assert.False(t, schema.Interconnect("roce").Valid())
	})
	t.Run("price_basis_and_stat", func(t *testing.T) {
		assert.Equal(t, "per_gpu_hour", string(schema.PriceBasisPerGPUHour))
		assert.Equal(t, "per_node_hour", string(schema.PriceBasisPerNodeHour))
		assert.False(t, schema.PriceBasis("per_hour").Valid())
		assert.Equal(t, "list", string(schema.PriceStatList))
		assert.Equal(t, "min", string(schema.PriceStatMin))
		assert.Equal(t, "p50", string(schema.PriceStatP50))
		assert.False(t, schema.PriceStat("mean").Valid())
	})
	t.Run("region_bucket", func(t *testing.T) {
		want := []string{"us-east", "us-west", "us-central", "ca", "eu-west", "eu-central", "eu-north",
			"uk", "in", "sg", "jp", "au", "me", "sa", "global", "unknown"}
		got := []schema.RegionBucket{schema.RegionUSEast, schema.RegionUSWest, schema.RegionUSCentral,
			schema.RegionCA, schema.RegionEUWest, schema.RegionEUCentral, schema.RegionEUNorth, schema.RegionUK,
			schema.RegionIN, schema.RegionSG, schema.RegionJP, schema.RegionAU, schema.RegionME, schema.RegionSA,
			schema.RegionGlobal, schema.RegionUnknown}
		require.Len(t, got, len(want))
		for i, g := range got {
			assert.Equal(t, want[i], string(g))
			assert.True(t, g.Valid())
		}
		assert.False(t, schema.RegionBucket("us").Valid(), "us and eu fallbacks are proposed, not adopted")
	})
	t.Run("run_statuses", func(t *testing.T) {
		assert.Equal(t, "ok", string(schema.RunStatusOK))
		assert.Equal(t, "partial", string(schema.RunStatusPartial))
		assert.Equal(t, "failed", string(schema.RunStatusFailed))
		assert.False(t, schema.RunStatus("timeout").Valid())
		assert.Equal(t, "ok", string(schema.SourceRunOK))
		assert.Equal(t, "failed", string(schema.SourceRunFailed))
		assert.Equal(t, "skipped", string(schema.SourceRunSkipped))
		assert.False(t, schema.SourceRunStatus("timeout").Valid(), "a timeout is a failed source with an error")
	})
}

func TestMethodologyVersion(t *testing.T) {
	assert.Equal(t, "0.1.0", schema.MethodologyVersion)
}
