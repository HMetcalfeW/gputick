// Package store defines where gputick writes its artifacts and how they are
// laid out. Implementations live in subpackages: fs for the local filesystem
// and, later, r2 for Cloudflare R2. The layout here is the one documented in
// docs/SCHEMA.md section 5 and is frozen within a methodology major version.
package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/HMetcalfeW/gputick/internal/source"
	"github.com/HMetcalfeW/gputick/pkg/schema"
)

// Store writes the three artifacts of a collection run. Each method returns
// the object key it wrote, relative to the store root, so the run manifest
// can record it.
type Store interface {
	// WriteRaw writes one source's raw snapshot. Raw objects are write-once;
	// writing to a key that exists returns ErrExists.
	WriteRaw(ctx context.Context, runID, sourceName string, raw source.RawResult) (string, error)
	// WriteObservations writes the run's observations. Observations may be
	// re-derived, so an existing key is overwritten.
	WriteObservations(ctx context.Context, runID string, obs []schema.Observation) (string, error)
	// WriteManifest writes the run manifest. Manifests are write-once.
	WriteManifest(ctx context.Context, m schema.RunManifest) (string, error)
}

// ErrExists is returned when a write-once object already exists.
var ErrExists = errors.New("object already exists")

var sourceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// RawPath is raw/source=<source>/date=YYYY-MM-DD/hour=HH/<run_id>.parquet.
func RawPath(sourceName, runID string) (string, error) {
	if !sourceNamePattern.MatchString(sourceName) {
		return "", fmt.Errorf("source name %q must be lowercase ASCII letters, digits and hyphens", sourceName)
	}
	t, err := schema.RunIDTime(runID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("raw/source=%s/date=%s/hour=%s/%s.parquet",
		sourceName, t.Format("2006-01-02"), t.Format("15"), runID), nil
}

// ObservationsPath is obs/date=YYYY-MM-DD/<run_id>.parquet.
func ObservationsPath(runID string) (string, error) {
	t, err := schema.RunIDTime(runID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("obs/date=%s/%s.parquet", t.Format("2006-01-02"), runID), nil
}

// ManifestPath is manifest/date=YYYY-MM-DD/<run_id>.json.
func ManifestPath(runID string) (string, error) {
	t, err := schema.RunIDTime(runID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("manifest/date=%s/%s.json", t.Format("2006-01-02"), runID), nil
}
