package store_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/internal/source"
	"github.com/HMetcalfeW/gputick/internal/store"
	"github.com/HMetcalfeW/gputick/pkg/schema"
)

const (
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

func twoPartResult() source.RawResult {
	return source.RawResult{Parts: []source.Part{
		{
			Ref: lambdaRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t1, Bytes: 100, SHA256: shaA,
			Header: []string{"InstanceType", "Price", "Region"},
			Rows: []source.RawRow{
				{"InstanceType": "gpu_8x_h100_sxm5", "Price": "23.92", "Region": "us-east-1"},
				{"InstanceType": "gpu_1x_a10", "Price": "0.75", "Region": ""},
			},
		},
		{
			Ref: runpodRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t2, Bytes: 50, SHA256: shaB,
			Header: []string{"InstanceType", "Price", "SpotPrice"},
			Rows: []source.RawRow{
				{"InstanceType": "1x_H100_SECURE", "Price": "2.99", "SpotPrice": "1.50"},
			},
		},
	}}
}

func encodeRaw(t *testing.T, raw source.RawResult) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, store.EncodeRaw(&buf, runID, "skypilot", raw))
	return buf.Bytes()
}

// readRaw decodes a raw parquet file: column names in file order, one map per
// row (strings, int64 timestamps, nil for nulls), and the metadata record.
func readRaw(t *testing.T, data []byte) ([]string, []map[string]any, store.RawMetadata) {
	t.Helper()
	pf, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
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

func TestEncodeRaw_RoundTrip(t *testing.T) {
	names, rows, meta := readRaw(t, encodeRaw(t, twoPartResult()))
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
			{Ref: lambdaRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t1.Truncate(time.Microsecond), Bytes: 100, SHA256: shaA,
				Header: []string{"InstanceType", "Price", "Region"}, Columns: []string{"InstanceType", "Price", "Region"}, Rows: 2},
			{Ref: runpodRef, UpstreamVersion: "v8/a1b2c3d", FetchedAt: t2, Bytes: 50, SHA256: shaB,
				Header: []string{"InstanceType", "Price", "SpotPrice"}, Columns: []string{"InstanceType", "Price", "SpotPrice"}, Rows: 1},
		},
	}, meta)
}

func TestEncodeRaw_RenamesAreRecorded(t *testing.T) {
	header := []string{"source", "", "price", "price", "payload_source"}
	columns := source.NormalizeHeader(header)
	require.Equal(t, []string{"source", "unnamed_1", "price", "price_2", "payload_source"}, columns)
	raw := source.RawResult{Parts: []source.Part{{
		Ref: "r", FetchedAt: t1.Add(123456789 * time.Nanosecond), SHA256: shaA, Header: header,
		Rows: []source.RawRow{{"source": "upstream-says-aws", "unnamed_1": "blank", "price": "1", "price_2": "2", "payload_source": "x"}},
	}}}

	names, rows, meta := readRaw(t, encodeRaw(t, raw))
	assert.Equal(t, append(envelope, "payload_source", "unnamed_1", "price", "price_2", "payload_payload_source"), names)
	require.Len(t, rows, 1)
	assert.Equal(t, "skypilot", rows[0]["source"], "the envelope keeps its name")
	assert.Equal(t, "upstream-says-aws", rows[0]["payload_source"])
	assert.Equal(t, "2", rows[0]["price_2"])
	assert.Equal(t, "x", rows[0]["payload_payload_source"], "an upstream name that is itself taken is prefixed again")
	assert.Nil(t, rows[0]["upstream_version"], "an empty upstream version is null")

	assert.Equal(t, header, meta.Parts[0].Header, "the header is kept exactly as received")
	assert.Equal(t, columns, meta.Parts[0].Columns)
	assert.Equal(t, []store.RawColumn{
		{Upstream: "source", Column: "payload_source"}, {Upstream: "unnamed_1", Column: "unnamed_1"},
		{Upstream: "price", Column: "price"}, {Upstream: "price_2", Column: "price_2"},
		{Upstream: "payload_source", Column: "payload_payload_source"},
	}, meta.Columns, "the metadata resolves every rename")
	assert.Equal(t, t1.Add(123456*time.Microsecond), meta.Parts[0].FetchedAt, "metadata matches the column's microsecond precision")
}

func TestEncodeRaw_NoRowsStillWritesSchemaAndMetadata(t *testing.T) {
	raw := source.RawResult{Parts: []source.Part{{Ref: "r", FetchedAt: t1, SHA256: shaA, Header: []string{"a"}}}}
	names, rows, meta := readRaw(t, encodeRaw(t, raw))
	assert.Equal(t, append(envelope, "a"), names)
	assert.Empty(t, rows)
	assert.Equal(t, 0, meta.Parts[0].Rows)
}

func TestEncodeRaw_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *source.RawResult)
		wantErr string
	}{
		{"zero fetched_at", func(r *source.RawResult) { r.Parts[1].FetchedAt = time.Time{} }, "part 1 (" + runpodRef + "): fetched_at is zero"},
		{"local fetched_at", func(r *source.RawResult) { r.Parts[0].FetchedAt = t1.In(time.FixedZone("EDT", -4*3600)) }, "fetched_at must be UTC"},
		{"row key outside columns", func(r *source.RawResult) { r.Parts[0].Rows[0]["Zone"] = "lost" }, `key "Zone" that is not in columns`},
		{"missing sha", func(r *source.RawResult) { r.Parts[0].SHA256 = "" }, "sha256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := twoPartResult()
			tt.mutate(&raw)
			var buf bytes.Buffer
			err := store.EncodeRaw(&buf, runID, "skypilot", raw)
			require.ErrorContains(t, err, "invalid raw result")
			require.ErrorContains(t, err, tt.wantErr)
			assert.Zero(t, buf.Len(), "nothing is written for invalid input")
		})
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

func readObservations(t *testing.T, data []byte) []schema.Observation {
	t.Helper()
	pf, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
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

func TestEncodeObservations_RoundTrip(t *testing.T) {
	a := observation()
	b := observation()
	b.Provider, b.Source = "aws", "aws"
	b.ZoneRaw = ptr("us-east-1a")
	b.UpstreamVersion = ptr("v8/a1b2c3d")
	b.SetAvailability(schema.Availability{Signal: schema.SignalCapacityFlag, Value: ptr("true"), Available: ptr(true)})

	var buf bytes.Buffer
	require.NoError(t, store.EncodeObservations(&buf, runID, []schema.Observation{a, b}))
	got := readObservations(t, buf.Bytes())
	require.Len(t, got, 2)
	assert.Equal(t, a, got[0])
	assert.Equal(t, b, got[1])

	buf.Reset()
	require.NoError(t, store.EncodeObservations(&buf, runID, nil))
	assert.Empty(t, readObservations(t, buf.Bytes()))
}

func TestEncodeObservations_RejectsInvariantViolations(t *testing.T) {
	o := observation()
	o.Stale, o.StaleSince = true, ptr(t1)
	var buf bytes.Buffer
	err := store.EncodeObservations(&buf, runID, []schema.Observation{observation(), o})
	require.ErrorContains(t, err, "observation 1: stale rows belong in rollups")
	assert.Zero(t, buf.Len())
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

func TestEncodeManifest(t *testing.T) {
	m := manifest()
	var buf bytes.Buffer
	require.NoError(t, store.EncodeManifest(&buf, m))
	var got schema.RunManifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, m.RunID, got.RunID)
	assert.Equal(t, m.Sources, got.Sources)
	text := buf.String()
	assert.Contains(t, text, "\n  \"run_id\"", "manifests are indented for humans")
	assert.Contains(t, text, `"started_at": "2026-10-24T12:00:00Z"`, "timestamps are UTC with a Z suffix")
	for _, null := range []string{`"raw_path": null`, `"error": null`, `"upstream_sha256": null`, `"upstream_version": null`} {
		assert.Contains(t, text, null, "null fields are present, not omitted")
	}
}

func TestEncodeManifest_RejectsInvalid(t *testing.T) {
	var buf bytes.Buffer
	require.ErrorContains(t, store.EncodeManifest(&buf, schema.RunManifest{RunID: runID}), "invalid manifest")
	m := manifest()
	m.StartedAt = m.StartedAt.In(time.FixedZone("EDT", -4*3600))
	require.ErrorContains(t, store.EncodeManifest(&buf, m), "started_at must be UTC")
	assert.Zero(t, buf.Len())
}
