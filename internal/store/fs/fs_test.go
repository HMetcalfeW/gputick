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

const (
	runID     = "20261024T120000Z-3f9a1c2e"
	lambdaRef = "https://raw.githubusercontent.com/skypilot-org/skypilot-catalog/x/catalogs/v8/lambda/vms.csv"
	runpodRef = "https://raw.githubusercontent.com/skypilot-org/skypilot-catalog/x/catalogs/v8/runpod/vms.csv"
)

var (
	t0   = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	t1   = time.Date(2026, 10, 24, 12, 0, 1, 500000, time.UTC)
	t2   = time.Date(2026, 10, 24, 12, 0, 7, 0, time.UTC)
	shaA = source.HashBody([]byte("lambda body"))
	shaB = source.HashBody([]byte("runpod body"))

	envelope = []string{"source", "run_id", "fetched_at", "upstream_version", "upstream_ref"}
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
			Ref: lambdaRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t1, Bytes: 100, SHA256: shaA,
			Columns: []string{"InstanceType", "Price", "Region"},
			Rows: []source.RawRow{
				{"InstanceType": "gpu_8x_h100_sxm5", "Price": "23.92", "Region": "us-east-1"},
				{"InstanceType": "gpu_1x_a10", "Price": "0.75", "Region": ""},
			},
		},
		{
			Ref: runpodRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t2, Bytes: 50, SHA256: shaB,
			Columns: []string{"InstanceType", "Price", "SpotPrice"},
			Rows: []source.RawRow{
				{"InstanceType": "1x_H100_SECURE", "Price": "2.99", "SpotPrice": "1.50"},
			},
		},
	}}
}

// readRaw reads a raw parquet file back: column names in file order, one map
// per row (strings, int64 timestamps, nil for nulls), and the metadata record.
func readRaw(t *testing.T, path string) ([]string, []map[string]any, store.RawMetadata) {
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

	var meta store.RawMetadata
	found := false
	for _, kv := range pf.Metadata().KeyValueMetadata {
		if kv.Key == store.RawMetadataKey {
			require.NoError(t, json.Unmarshal([]byte(kv.Value), &meta))
			found = true
		}
	}
	require.True(t, found, "raw file carries %s metadata", store.RawMetadataKey)

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
	return names, out, meta
}

func TestWriteRaw_RoundTrip(t *testing.T) {
	s := newStore(t)
	key, err := s.WriteRaw(context.Background(), runID, "skypilot", twoPartResult())
	require.NoError(t, err)
	assert.Equal(t, "raw/source=skypilot/date=2026-10-24/hour=12/"+runID+".parquet", key)

	names, rows, meta := readRaw(t, filepath.Join(s.Root(), key))
	assert.Equal(t, append(envelope, "InstanceType", "Price", "Region", "SpotPrice"), names,
		"envelope first, then upstream columns in header order, union across parts")
	require.Len(t, rows, 3)

	assert.Equal(t, map[string]any{
		"source": "skypilot", "run_id": runID, "fetched_at": t1.UnixMicro(),
		"upstream_version": "v8/a1b2c3d", "upstream_ref": lambdaRef,
		"InstanceType": "gpu_8x_h100_sxm5", "Price": "23.92", "Region": "us-east-1", "SpotPrice": nil,
	}, rows[0])
	region, ok := rows[1]["Region"].(string)
	require.True(t, ok, "an empty upstream value stays a string, not null")
	assert.Empty(t, region)
	assert.Nil(t, rows[1]["SpotPrice"], "a column the part did not have is null")

	assert.Equal(t, t2.UnixMicro(), rows[2]["fetched_at"], "each row carries its own part's fetched_at")
	assert.Nil(t, rows[2]["Region"])
	assert.Equal(t, "1.50", rows[2]["SpotPrice"])
	assert.Equal(t, runpodRef, rows[2]["upstream_ref"])

	assert.Equal(t, store.RawMetadata{
		Source: "skypilot", RunID: runID,
		Columns: []store.RawColumn{{Upstream: "InstanceType", Column: "InstanceType"}, {Upstream: "Price", Column: "Price"},
			{Upstream: "Region", Column: "Region"}, {Upstream: "SpotPrice", Column: "SpotPrice"}},
		Parts: []store.RawPart{
			{Ref: lambdaRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t1, Bytes: 100, SHA256: shaA,
				Columns: []string{"InstanceType", "Price", "Region"}, Rows: 2},
			{Ref: runpodRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t2, Bytes: 50, SHA256: shaB,
				Columns: []string{"InstanceType", "Price", "SpotPrice"}, Rows: 1},
		},
	}, meta)
}

func TestWriteRaw_EnvelopeCollisionsAreRenamedAndRecorded(t *testing.T) {
	s := newStore(t)
	header := source.NormalizeHeader([]string{"source", "", "price", "payload_source"})
	require.Equal(t, []string{"source", "unnamed_1", "price", "payload_source"}, header)
	raw := source.RawResult{Parts: []source.Part{{
		Ref: "r", FetchedAt: t1, SHA256: shaA, Columns: header,
		Rows: []source.RawRow{{"source": "upstream-says-aws", "unnamed_1": "blank", "price": "1", "payload_source": "x"}},
	}}}
	key, err := s.WriteRaw(context.Background(), runID, "gpuhunt", raw)
	require.NoError(t, err)

	names, rows, meta := readRaw(t, filepath.Join(s.Root(), key))
	assert.Equal(t, append(envelope, "payload_source", "unnamed_1", "price", "payload_payload_source"), names)
	require.Len(t, rows, 1)
	assert.Equal(t, "gpuhunt", rows[0]["source"], "the envelope keeps its name")
	assert.Equal(t, "upstream-says-aws", rows[0]["payload_source"])
	assert.Equal(t, "x", rows[0]["payload_payload_source"], "an upstream name that is itself taken is prefixed again")
	assert.Nil(t, rows[0]["upstream_version"], "an empty upstream version is null")
	assert.Equal(t, []store.RawColumn{
		{Upstream: "source", Column: "payload_source"}, {Upstream: "unnamed_1", Column: "unnamed_1"},
		{Upstream: "price", Column: "price"}, {Upstream: "payload_source", Column: "payload_payload_source"},
	}, meta.Columns, "the metadata resolves every rename")
}

func TestWriteRaw_NoRowsStillWritesSchemaAndMetadata(t *testing.T) {
	s := newStore(t)
	raw := source.RawResult{Parts: []source.Part{{Ref: "r", FetchedAt: t1, SHA256: shaA, Columns: []string{"a"}}}}
	key, err := s.WriteRaw(context.Background(), runID, "lambda", raw)
	require.NoError(t, err)
	names, rows, meta := readRaw(t, filepath.Join(s.Root(), key))
	assert.Equal(t, append(envelope, "a"), names)
	assert.Empty(t, rows)
	assert.Equal(t, 0, meta.Parts[0].Rows)
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

func TestWriteRaw_RejectsInvalidInput(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tests := []struct {
		name    string
		runID   string
		source  string
		mutate  func(r *source.RawResult)
		wantErr string
	}{
		{"bad run id", "bad-run-id", "skypilot", nil, "does not match"},
		{"bad source name", runID, "Sky Pilot", nil, "lowercase ASCII"},
		{"zero fetched_at", runID, "skypilot", func(r *source.RawResult) { r.Parts[1].FetchedAt = time.Time{} }, "part 1 (" + runpodRef + "): fetched_at is zero"},
		{"empty column", runID, "skypilot", func(r *source.RawResult) { r.Parts[0].Columns[1] = "" }, "column 1 is empty"},
		{"row key outside columns", runID, "skypilot", func(r *source.RawResult) { r.Parts[0].Rows[0]["Zone"] = "lost" }, `key "Zone" that is not in columns`},
		{"missing sha", runID, "skypilot", func(r *source.RawResult) { r.Parts[0].SHA256 = "" }, "sha256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := twoPartResult()
			if tt.mutate != nil {
				tt.mutate(&raw)
			}
			_, err := s.WriteRaw(ctx, tt.runID, tt.source, raw)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
	assertNoFiles(t, s.Root())
}

func TestWriteRaw_CanceledContextWritesNothing(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.WriteRaw(ctx, runID, "skypilot", twoPartResult())
	require.ErrorIs(t, err, context.Canceled)
	assertNoFiles(t, s.Root())
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
		require.Len(t, p, 1, "every column is top level: %v", p)
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
	b.UpstreamVersion = ptr("v8/a1b2c3d")
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

func TestWriteObservations_RejectsInvariantViolations(t *testing.T) {
	s := newStore(t)
	tests := []struct {
		name    string
		mutate  func(o *schema.Observation)
		wantErr string
	}{
		{"stale row", func(o *schema.Observation) { o.Stale, o.StaleSince = true, ptr(t1) }, "stale rows belong in rollups"},
		{"foreign run", func(o *schema.Observation) { o.RunID = "20261024T110000Z-00000000" }, `run_id "20261024T110000Z-00000000" does not belong to run`},
		{"invalid row", func(o *schema.Observation) { o.FormFactor = "SXM" }, `form_factor "SXM"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := observation()
			tt.mutate(&o)
			_, err := s.WriteObservations(context.Background(), runID, []schema.Observation{observation(), o})
			require.Error(t, err)
			assert.ErrorContains(t, err, "observation 1: "+tt.wantErr)
		})
	}
	assertNoFiles(t, s.Root())
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

func manifest() schema.RunManifest {
	return schema.RunManifest{
		RunID: runID, StartedAt: t0, FinishedAt: t2, CollectorVersion: "gputick dev",
		MethodologyVersion: schema.MethodologyVersion, Status: schema.RunStatusOK,
		ObservationsPath: ptr("obs/date=2026-10-24/" + runID + ".parquet"), ObservationsTotal: 2,
		Sources: []schema.SourceRun{{Source: "skypilot", Status: schema.SourceRunOK, StartedAt: t0, FinishedAt: t2,
			DurationMillis: 7000, UnmappedRegions: []string{}}},
	}
}

func TestWriteManifest(t *testing.T) {
	s := newStore(t)
	m := manifest()
	key, err := s.WriteManifest(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, "manifest/date=2026-10-24/"+runID+".json", key)

	data, err := os.ReadFile(filepath.Join(s.Root(), key))
	require.NoError(t, err)
	var got schema.RunManifest
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, m.RunID, got.RunID)
	assert.Equal(t, m.Sources, got.Sources)
	text := string(data)
	assert.Contains(t, text, "\n  \"run_id\"", "manifests are indented for humans")
	assert.Contains(t, text, `"started_at": "2026-10-24T12:00:00Z"`, "timestamps are UTC with a Z suffix")
	for _, null := range []string{`"raw_path": null`, `"error": null`, `"upstream_sha256": null`, `"upstream_version": null`} {
		assert.Contains(t, text, null, "null fields are present, not omitted")
	}

	_, err = s.WriteManifest(context.Background(), m)
	require.ErrorIs(t, err, store.ErrExists)
}

func TestWriteManifest_RejectsInvalid(t *testing.T) {
	s := newStore(t)
	_, err := s.WriteManifest(context.Background(), schema.RunManifest{RunID: "nope"})
	require.Error(t, err)
	_, err = s.WriteManifest(context.Background(), schema.RunManifest{RunID: runID})
	require.ErrorContains(t, err, "invalid manifest")
	m := manifest()
	m.StartedAt = m.StartedAt.In(time.FixedZone("EDT", -4*3600))
	_, err = s.WriteManifest(context.Background(), m)
	require.ErrorContains(t, err, "started_at must be UTC")
	assertNoFiles(t, s.Root())
}

func TestWrite_LeavesNoTempFiles(t *testing.T) {
	s := newStore(t)
	_, err := s.WriteRaw(context.Background(), runID, "skypilot", twoPartResult())
	require.NoError(t, err)
	_, err = s.WriteManifest(context.Background(), manifest())
	require.NoError(t, err)
	var others []string
	err = filepath.WalkDir(s.Root(), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(path) != ".parquet" && filepath.Ext(path) != ".json" {
			others = append(others, path)
		}
		return err
	})
	require.NoError(t, err)
	assert.Empty(t, others)
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
	_, err = s.WriteManifest(ctx, manifest())
	assert.Error(t, err)
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

// assertNoFiles checks that nothing at all was published or left behind.
func assertNoFiles(t *testing.T, root string) {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return err
	})
	require.NoError(t, err)
	assert.Empty(t, files)
}
