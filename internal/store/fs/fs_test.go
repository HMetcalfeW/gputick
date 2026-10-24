package fs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/internal/source"
	"github.com/HMetcalfeW/gputick/internal/store"
	"github.com/HMetcalfeW/gputick/internal/store/fs"
	"github.com/HMetcalfeW/gputick/pkg/schema"
)

const runID = "20261024T120000Z-3f9a1c2e"

var (
	t1 = time.Date(2026, 10, 24, 12, 0, 1, 500000, time.UTC)
	t2 = time.Date(2026, 10, 24, 12, 0, 7, 0, time.UTC)
)

func ptr[T any](v T) *T { return &v }

func newStore(t *testing.T) *fs.Store {
	t.Helper()
	s, err := fs.New(filepath.Join(t.TempDir(), "data"))
	require.NoError(t, err)
	return s
}

func twoPartResult() source.RawResult {
	return source.RawResult{Parts: []source.Part{
		{
			Ref:             "https://raw.githubusercontent.com/skypilot-org/skypilot-catalog/x/catalogs/v7/lambda/vms.csv",
			UpstreamVersion: "v7/a1b2c3d", FetchedAt: t1, Bytes: 100, SHA256: "aa",
			Columns: []string{"InstanceType", "Price", "Region"},
			Rows: []source.RawRow{
				{"InstanceType": "gpu_8x_h100_sxm5", "Price": "23.92", "Region": "us-east-1"},
				{"InstanceType": "gpu_1x_a10", "Price": "0.75", "Region": ""},
			},
		},
		{
			Ref:             "https://raw.githubusercontent.com/skypilot-org/skypilot-catalog/x/catalogs/v7/runpod/vms.csv",
			UpstreamVersion: "v7/a1b2c3d", FetchedAt: t2, Bytes: 50, SHA256: "bb",
			Columns: []string{"InstanceType", "Price", "SpotPrice"},
			Rows: []source.RawRow{
				{"InstanceType": "1x_H100_SECURE", "Price": "2.99", "SpotPrice": "1.50"},
			},
		},
	}}
}

// readRaw reads a raw parquet file back as one map per row, keyed by column
// name, with strings, int64 timestamps and nil for nulls.
func readRaw(t *testing.T, path string) ([]string, []map[string]any) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	st, err := f.Stat()
	require.NoError(t, err)
	pf, err := parquet.OpenFile(f, st.Size())
	require.NoError(t, err)

	sch := pf.Schema()
	names := make([]string, len(sch.Columns()))
	for _, path := range sch.Columns() {
		leaf, ok := sch.Lookup(path...)
		require.True(t, ok)
		names[leaf.ColumnIndex] = path[0]
	}

	r := parquet.NewGenericReader[any](pf)
	t.Cleanup(func() { _ = r.Close() })
	var out []map[string]any
	buf := make([]parquet.Row, 8)
	for {
		n, err := r.ReadRows(buf)
		for _, row := range buf[:n] {
			m := map[string]any{}
			row.Range(func(ci int, vals []parquet.Value) bool {
				v := vals[0]
				switch {
				case v.IsNull():
					m[names[ci]] = nil
				case v.Kind() == parquet.Int64:
					m[names[ci]] = v.Int64()
				default:
					m[names[ci]] = v.String()
				}
				return true
			})
			out = append(out, m)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	return names, out
}

func TestWriteRaw_RoundTrip(t *testing.T) {
	s := newStore(t)
	key, err := s.WriteRaw(context.Background(), runID, "skypilot", twoPartResult())
	require.NoError(t, err)
	assert.Equal(t, "raw/source=skypilot/date=2026-10-24/hour=12/"+runID+".parquet", key)

	names, rows := readRaw(t, filepath.Join(s.Root(), key))
	assert.ElementsMatch(t, []string{"source", "run_id", "fetched_at", "upstream_version", "upstream_ref",
		"InstanceType", "Price", "Region", "SpotPrice"}, names)
	require.Len(t, rows, 3)

	assert.Equal(t, map[string]any{
		"source": "skypilot", "run_id": runID, "fetched_at": t1.UnixMicro(),
		"upstream_version": "v7/a1b2c3d",
		"upstream_ref":     "https://raw.githubusercontent.com/skypilot-org/skypilot-catalog/x/catalogs/v7/lambda/vms.csv",
		"InstanceType":     "gpu_8x_h100_sxm5", "Price": "23.92", "Region": "us-east-1", "SpotPrice": nil,
	}, rows[0])
	region, ok := rows[1]["Region"].(string)
	require.True(t, ok, "an empty upstream value stays a string, not null")
	assert.Empty(t, region)
	assert.Nil(t, rows[1]["SpotPrice"], "a column the part did not have is null")

	assert.Equal(t, t2.UnixMicro(), rows[2]["fetched_at"], "each row carries its own part's fetched_at")
	assert.Nil(t, rows[2]["Region"])
	assert.Equal(t, "1.50", rows[2]["SpotPrice"])
	assert.Contains(t, rows[2]["upstream_ref"], "/runpod/")
}

func TestWriteRaw_EnvelopeCollisionsAndOddHeaders(t *testing.T) {
	s := newStore(t)
	raw := source.RawResult{Parts: []source.Part{{
		Ref: "r", FetchedAt: t1,
		Columns: []string{"source", "", "price", "payload_source"},
		Rows:    []source.RawRow{{"source": "upstream-says-aws", "": "blank", "price": "1", "payload_source": "x"}},
	}}}
	key, err := s.WriteRaw(context.Background(), runID, "gpuhunt", raw)
	require.NoError(t, err)

	names, rows := readRaw(t, filepath.Join(s.Root(), key))
	assert.ElementsMatch(t, []string{"source", "run_id", "fetched_at", "upstream_version", "upstream_ref",
		"payload_source", "unnamed_1", "price", "payload_payload_source"}, names)
	require.Len(t, rows, 1)
	assert.Equal(t, "gpuhunt", rows[0]["source"], "the envelope keeps its name")
	assert.Equal(t, "upstream-says-aws", rows[0]["payload_source"])
	assert.Equal(t, "blank", rows[0]["unnamed_1"])
	assert.Equal(t, "x", rows[0]["payload_payload_source"], "an upstream name that is itself taken is prefixed again")
	assert.Nil(t, rows[0]["upstream_version"], "an empty upstream version is null")
}

func TestWriteRaw_NoRowsStillWritesSchema(t *testing.T) {
	s := newStore(t)
	raw := source.RawResult{Parts: []source.Part{{Ref: "r", FetchedAt: t1, Columns: []string{"a"}}}}
	key, err := s.WriteRaw(context.Background(), runID, "lambda", raw)
	require.NoError(t, err)
	names, rows := readRaw(t, filepath.Join(s.Root(), key))
	assert.Contains(t, names, "a")
	assert.Empty(t, rows)
}

func TestWriteRaw_IsWriteOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, err := s.WriteRaw(ctx, runID, "skypilot", twoPartResult())
	require.NoError(t, err)
	_, err = s.WriteRaw(ctx, runID, "skypilot", twoPartResult())
	require.ErrorIs(t, err, store.ErrExists)
	_, err = s.WriteRaw(ctx, runID, "gpuhunt", twoPartResult())
	assert.NoError(t, err, "a different source in the same run is a different key")
}

func TestWriteRaw_RejectsBadInput(t *testing.T) {
	s := newStore(t)
	_, err := s.WriteRaw(context.Background(), "bad-run-id", "skypilot", twoPartResult())
	require.Error(t, err)
	_, err = s.WriteRaw(context.Background(), runID, "Sky Pilot", twoPartResult())
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.WriteRaw(ctx, runID, "skypilot", twoPartResult())
	require.ErrorIs(t, err, context.Canceled)
	entries, err := os.ReadDir(s.Root())
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused write leaves nothing behind")
}

func observation() schema.Observation {
	return schema.Observation{
		ObservedAt: t2, RunID: runID, Source: "lambda", Provider: "lambda",
		GPUSlug: "nvidia-h100-sxm-80gb", GPUVendor: "nvidia", GPUModel: "h100",
		FormFactor: schema.FormFactorSXM, GPUMemoryGB: 80, Interconnect: schema.InterconnectUnknown,
		NodeGPUCount: 8, InstanceType: ptr("gpu_8x_h100_sxm5"), RegionRaw: "us-east-1",
		RegionBucket: schema.RegionUSEast, PriceType: schema.PriceTypeRateCard, Tier: "on_demand",
		PriceStat: schema.PriceStatList, PriceUSDPerGPUHour: 2.99, PriceBasis: schema.PriceBasisPerNodeHour,
		PriceOriginal: 23.92, CurrencyOriginal: "USD",
		AvailabilitySignal: schema.SignalCapacityFlag, AvailabilityValue: ptr("false"), Available: ptr(false),
		MethodologyVersion: schema.MethodologyVersion,
	}
}

func TestObservationSchema_MatchesSchemaDoc(t *testing.T) {
	sch := parquet.SchemaOf(schema.Observation{})
	names := make([]string, 0, len(sch.Columns()))
	for _, p := range sch.Columns() {
		require.Len(t, p, 1, "every column is top level, including the embedded Availability fields: %v", p)
		names = append(names, p[0])
	}
	assert.Equal(t, []string{
		"observed_at", "run_id", "source", "provider", "gpu_slug", "gpu_vendor", "gpu_model", "form_factor",
		"gpu_memory_gb", "interconnect", "node_gpu_count", "instance_type", "region_raw", "zone_raw",
		"region_bucket", "price_type", "tier", "price_stat", "price_usd_per_gpu_hour", "price_basis",
		"price_original", "currency_original", "fx_rate_to_usd", "min_commitment_hours", "bundled",
		"availability_signal", "availability_value", "available", "stale", "stale_since",
		"methodology_version", "upstream_version",
	}, names)

	for _, optional := range []string{"instance_type", "zone_raw", "fx_rate_to_usd", "availability_value",
		"available", "stale_since", "upstream_version"} {
		leaf, ok := sch.Lookup(optional)
		require.True(t, ok)
		assert.True(t, leaf.Node.Optional(), optional)
	}
	for _, required := range []string{"observed_at", "price_usd_per_gpu_hour", "stale", "availability_signal"} {
		leaf, ok := sch.Lookup(required)
		require.True(t, ok)
		assert.True(t, leaf.Node.Required(), required)
	}
	leaf, _ := sch.Lookup("observed_at")
	assert.Equal(t, parquet.Int64, leaf.Node.Type().Kind())
	assert.Contains(t, leaf.Node.Type().String(), "unit=MICROS")
}

func TestWriteObservations_RoundTripAndOverwrite(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := observation()
	b := observation()
	b.Provider, b.Source = "aws", "aws"
	b.ZoneRaw = ptr("us-east-1a")
	b.Stale, b.StaleSince = true, ptr(t1)
	b.SetAvailability(schema.Availability{Signal: schema.SignalNone})

	key, err := s.WriteObservations(ctx, runID, []schema.Observation{a, b})
	require.NoError(t, err)
	assert.Equal(t, "obs/date=2026-10-24/"+runID+".parquet", key)

	got := readObservations(t, filepath.Join(s.Root(), key))
	require.Len(t, got, 2)
	assert.Equal(t, a, got[0])
	assert.Equal(t, b, got[1])

	key2, err := s.WriteObservations(ctx, runID, []schema.Observation{a})
	require.NoError(t, err, "observations are re-derivable, so overwrite is allowed")
	assert.Equal(t, key, key2)
	assert.Len(t, readObservations(t, filepath.Join(s.Root(), key)), 1)
}

func TestWriteObservations_Empty(t *testing.T) {
	s := newStore(t)
	key, err := s.WriteObservations(context.Background(), runID, nil)
	require.NoError(t, err)
	assert.Empty(t, readObservations(t, filepath.Join(s.Root(), key)))
}

func readObservations(t *testing.T, path string) []schema.Observation {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	st, err := f.Stat()
	require.NoError(t, err)
	pf, err := parquet.OpenFile(f, st.Size())
	require.NoError(t, err)
	r := parquet.NewGenericReader[schema.Observation](pf)
	t.Cleanup(func() { _ = r.Close() })

	out := []schema.Observation{}
	buf := make([]schema.Observation, 4)
	for {
		n, err := r.Read(buf)
		for _, o := range buf[:n] {
			o.ObservedAt = o.ObservedAt.UTC()
			if o.StaleSince != nil {
				o.StaleSince = ptr(o.StaleSince.UTC())
			}
			out = append(out, o)
		}
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
	}
}

func TestWriteManifest(t *testing.T) {
	s := newStore(t)
	m := schema.RunManifest{
		RunID: runID, StartedAt: t1, FinishedAt: t2, CollectorVersion: "gputick dev",
		MethodologyVersion: schema.MethodologyVersion, Status: schema.RunStatusOK,
		ObservationsPath: ptr("obs/date=2026-10-24/" + runID + ".parquet"), ObservationsTotal: 2,
		Sources: []schema.SourceRun{{Source: "skypilot", Status: schema.SourceRunOK, StartedAt: t1, FinishedAt: t2,
			DurationMillis: 5500, UnmappedRegions: []string{}}},
	}
	key, err := s.WriteManifest(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, "manifest/date=2026-10-24/"+runID+".json", key)

	data, err := os.ReadFile(filepath.Join(s.Root(), key))
	require.NoError(t, err)
	var got schema.RunManifest
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, m.RunID, got.RunID)
	assert.Equal(t, m.Sources, got.Sources)
	assert.Contains(t, string(data), "\n  \"run_id\"", "manifests are indented for humans")

	_, err = s.WriteManifest(context.Background(), m)
	require.ErrorIs(t, err, store.ErrExists)
	_, err = s.WriteManifest(context.Background(), schema.RunManifest{RunID: "nope"})
	assert.Error(t, err)
}

func TestWrite_LeavesNoTempFiles(t *testing.T) {
	s := newStore(t)
	_, err := s.WriteRaw(context.Background(), runID, "skypilot", twoPartResult())
	require.NoError(t, err)
	var tmp []string
	err = filepath.WalkDir(s.Root(), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(path) != ".parquet" {
			tmp = append(tmp, path)
		}
		return err
	})
	require.NoError(t, err)
	assert.Empty(t, tmp)
}

func TestNew_RejectsEmptyRoot(t *testing.T) {
	_, err := fs.New("")
	assert.Error(t, err)
}

func TestNew_RootUnderRegularFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	_, err := fs.New(filepath.Join(file, "data"))
	assert.Error(t, err)
}

func TestWrite_TargetDirectoryIsAFile(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, prefix := range []string{"raw", "obs", "manifest"} {
		require.NoError(t, os.WriteFile(filepath.Join(s.Root(), prefix), []byte("x"), 0o600))
	}
	_, err := s.WriteRaw(ctx, runID, "skypilot", twoPartResult())
	require.Error(t, err)
	_, err = s.WriteObservations(ctx, runID, []schema.Observation{observation()})
	require.Error(t, err)
	_, err = s.WriteManifest(ctx, schema.RunManifest{RunID: runID})
	assert.Error(t, err)
}
