package fs_test

import (
	"bytes"
	"context"
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
	t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	t2 = time.Date(2026, 10, 24, 12, 0, 7, 0, time.UTC)
)

func ptr[T any](v T) *T { return &v }

func newStore(t *testing.T) *fs.Store {
	t.Helper()
	s, err := fs.New(filepath.Join(t.TempDir(), "data"))
	require.NoError(t, err)
	return s
}

func rawResult() source.RawResult {
	return source.RawResult{Parts: []source.Part{{
		Ref: "https://example.test/lambda/vms.csv", UpstreamVersion: "v8/a1b2c3d", FetchedAt: t2, Bytes: 100,
		SHA256: source.HashBody([]byte("body")), Header: []string{"InstanceType", "Price"},
		Rows: []source.RawRow{{"InstanceType": "gpu_8x_h100_sxm5", "Price": "23.92"}, {"InstanceType": "gpu_1x_a10", "Price": "0.75"}},
	}}}
}

func observation() schema.Observation {
	return schema.Observation{
		ObservedAt: t2, RunID: runID, Source: "lambda", Provider: "lambda",
		GPUSlug: "nvidia-h100-sxm-80gb", GPUVendor: "nvidia", GPUModel: "h100",
		FormFactor: schema.FormFactorSXM, GPUMemoryGB: 80, Interconnect: schema.InterconnectUnknown,
		NodeGPUCount: 8, RegionRaw: "us-east-1", RegionBucket: schema.RegionUSEast,
		PriceType: schema.PriceTypeRateCard, Tier: "on_demand", PriceStat: schema.PriceStatList,
		PriceUSDPerGPUHour: 2.99, PriceBasis: schema.PriceBasisPerNodeHour, PriceOriginal: 23.92,
		CurrencyOriginal: "USD", AvailabilitySignal: schema.SignalNone, MethodologyVersion: schema.MethodologyVersion,
	}
}

func manifest() schema.RunManifest {
	return schema.RunManifest{
		RunID: runID, StartedAt: t0, FinishedAt: t2, CollectorVersion: "gputick dev",
		MethodologyVersion: schema.MethodologyVersion, Status: schema.RunStatusOK,
		ObservationsPath: ptr("obs/date=2026-10-24/" + runID + ".parquet"), ObservationsTotal: 1,
		Sources: []schema.SourceRun{{Source: "lambda", Status: schema.SourceRunOK, StartedAt: t0, FinishedAt: t2,
			DurationMillis: 7000, UnmappedRegions: []string{}}},
	}
}

// openParquet opens a published parquet object and returns its row count and
// whether it carries the raw metadata entry.
func openParquet(t *testing.T, path string) (int64, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	pf, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	hasMeta := false
	for _, kv := range pf.Metadata().KeyValueMetadata {
		if kv.Key == store.RawMetadataKey {
			hasMeta = true
		}
	}
	return pf.NumRows(), hasMeta
}

// publishedFiles lists every regular file under root, relative, in slash form.
func publishedFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return err
	})
	require.NoError(t, err)
	return out
}

func TestWriteRaw_PublishesParquetWithMetadata(t *testing.T) {
	s := newStore(t)
	key, err := s.WriteRaw(context.Background(), runID, "lambda", rawResult())
	require.NoError(t, err)
	assert.Equal(t, "raw/source=lambda/date=2026-10-24/hour=12/"+runID+".parquet", key)
	rows, hasMeta := openParquet(t, filepath.Join(s.Root(), key))
	assert.EqualValues(t, 2, rows)
	assert.True(t, hasMeta)
	assert.Equal(t, []string{key}, publishedFiles(t, s.Root()), "nothing but the object, no temp files")
}

func TestWriteRaw_IsWriteOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, err := s.WriteRaw(ctx, runID, "lambda", rawResult())
	require.NoError(t, err)
	_, err = s.WriteRaw(ctx, runID, "lambda", rawResult())
	require.ErrorIs(t, err, store.ErrExists)
	_, err = s.WriteRaw(ctx, runID, "gpuhunt", rawResult())
	assert.NoError(t, err, "a different source in the same run is a different key")
}

func TestWriteRaw_RejectsBadInputWithoutWriting(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, err := s.WriteRaw(ctx, "bad-run-id", "lambda", rawResult())
	require.ErrorContains(t, err, "does not match")
	_, err = s.WriteRaw(ctx, runID, "Sky Pilot", rawResult())
	require.ErrorContains(t, err, "lowercase ASCII")
	raw := rawResult()
	raw.Parts[0].Rows[0]["Zone"] = "lost"
	_, err = s.WriteRaw(ctx, runID, "lambda", raw)
	require.ErrorContains(t, err, "invalid raw result")

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.WriteRaw(canceled, runID, "lambda", rawResult())
	require.ErrorIs(t, err, context.Canceled)

	assert.Empty(t, publishedFiles(t, s.Root()))
	_, err = os.Stat(filepath.Join(s.Root(), "raw"))
	assert.ErrorIs(t, err, os.ErrNotExist, "a refused write does not even create the partition directory")
}

func TestWriteObservations_WritesAndOverwrites(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	key, err := s.WriteObservations(ctx, runID, []schema.Observation{observation(), observation()})
	require.NoError(t, err)
	assert.Equal(t, "obs/date=2026-10-24/"+runID+".parquet", key)
	rows, _ := openParquet(t, filepath.Join(s.Root(), key))
	assert.EqualValues(t, 2, rows)

	key2, err := s.WriteObservations(ctx, runID, []schema.Observation{observation()})
	require.NoError(t, err, "observations are re-derivable, so overwrite is allowed")
	assert.Equal(t, key, key2)
	rows, _ = openParquet(t, filepath.Join(s.Root(), key))
	assert.EqualValues(t, 1, rows)

	stale := observation()
	stale.Stale, stale.StaleSince = true, ptr(t2)
	_, err = s.WriteObservations(ctx, runID, []schema.Observation{stale})
	require.ErrorContains(t, err, "stale rows belong in rollups")
	rows, _ = openParquet(t, filepath.Join(s.Root(), key))
	assert.EqualValues(t, 1, rows, "a refused overwrite leaves the previous object intact")
}

func TestWriteManifest(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	key, err := s.WriteManifest(ctx, manifest())
	require.NoError(t, err)
	assert.Equal(t, "manifest/date=2026-10-24/"+runID+".json", key)
	data, err := os.ReadFile(filepath.Join(s.Root(), key))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"started_at": "2026-10-24T12:00:00Z"`)

	_, err = s.WriteManifest(ctx, manifest())
	require.ErrorIs(t, err, store.ErrExists)
	_, err = s.WriteManifest(ctx, schema.RunManifest{RunID: "nope"})
	require.Error(t, err)
	_, err = s.WriteManifest(ctx, schema.RunManifest{RunID: "20261024T130000Z-00000000"})
	require.ErrorContains(t, err, "invalid manifest")
	assert.Equal(t, []string{key}, publishedFiles(t, s.Root()))
}

func TestWrite_TargetDirectoryIsAFile(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, prefix := range []string{"raw", "obs", "manifest"} {
		require.NoError(t, os.WriteFile(filepath.Join(s.Root(), prefix), []byte("x"), 0o600))
	}
	_, err := s.WriteRaw(ctx, runID, "lambda", rawResult())
	require.Error(t, err)
	_, err = s.WriteObservations(ctx, runID, []schema.Observation{observation()})
	require.Error(t, err)
	_, err = s.WriteManifest(ctx, manifest())
	require.Error(t, err)
	entries, err := os.ReadDir(filepath.Join(s.Root(), "tmp"))
	require.NoError(t, err)
	assert.Empty(t, entries, "failed publishes clean up their temp files")
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
