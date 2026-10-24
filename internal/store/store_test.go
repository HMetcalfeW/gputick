package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/internal/store"
)

const runID = "20261024T120000Z-3f9a1c2e"

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

func TestPaths_RejectBadInput(t *testing.T) {
	for _, name := range []string{"", "SkyPilot", "sky pilot", "sky_pilot", "-sky", "sky/pilot"} {
		_, err := store.RawPath(name, runID)
		require.Error(t, err, name)
	}
	for _, id := range []string{"", "run-1", "20261024T120000Z-3F9A1C2E"} {
		_, err := store.RawPath("skypilot", id)
		require.Error(t, err, id)
		_, err = store.ObservationsPath(id)
		require.Error(t, err, id)
		_, err = store.ManifestPath(id)
		assert.Error(t, err, id)
	}
}
