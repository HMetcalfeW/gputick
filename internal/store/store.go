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
//
// Implementations validate their input before writing anything: the raw
// result, every observation and the manifest must pass their Validate
// methods, observations must carry the run's id and must not be stale.
// A context canceled before the object is published aborts the write and
// leaves nothing behind, so a caller that must not lose a snapshot to a fetch
// deadline writes with a context of its own.
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

var (
	// ErrExists is returned when a write-once object already exists.
	ErrExists = errors.New("object already exists")

	sourceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

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

// ValidateObservations checks what every store requires of an obs/ file: each
// row validates, belongs to runID, and is not stale. Stale rows exist only in
// rollups (docs/METHODOLOGY.md section 9).
func ValidateObservations(runID string, obs []schema.Observation) error {
	for i := range obs {
		o := &obs[i]
		if err := o.Validate(); err != nil {
			return fmt.Errorf("observation %d: %w", i, err)
		}
		if o.RunID != runID {
			return fmt.Errorf("observation %d: run_id %q does not belong to run %q", i, o.RunID, runID)
		}
		if o.Stale {
			return fmt.Errorf("observation %d: stale rows belong in rollups, not obs/", i)
		}
	}
	return nil
}
