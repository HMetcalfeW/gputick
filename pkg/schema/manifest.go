package schema

import (
	"errors"
	"fmt"
	"time"
)

// RunManifest is written once per collection run after every source has
// finished or failed. It is the durable audit trail for what happened in a run.
type RunManifest struct {
	RunID              string      `json:"run_id"`
	StartedAt          time.Time   `json:"started_at"`
	FinishedAt         time.Time   `json:"finished_at"`
	CollectorVersion   string      `json:"collector_version"`
	MethodologyVersion string      `json:"methodology_version"`
	Status             RunStatus   `json:"status"`
	ObservationsPath   *string     `json:"observations_path"`
	ObservationsTotal  int32       `json:"observations_total"`
	Sources            []SourceRun `json:"sources"`
}

// SourceRun is one source's outcome within a run.
type SourceRun struct {
	Source           string          `json:"source"`
	Status           SourceRunStatus `json:"status"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       time.Time       `json:"finished_at"`
	DurationMillis   int32           `json:"duration_ms"`
	UpstreamVersion  *string         `json:"upstream_version"`
	UpstreamSHA256   *string         `json:"upstream_sha256"`
	RawPath          *string         `json:"raw_path"`
	RowsRaw          int32           `json:"rows_raw"`
	RowsObservations int32           `json:"rows_observations"`
	RowsUnmappedGPU  int32           `json:"rows_unmapped_gpu"`
	UnmappedRegions  []string        `json:"unmapped_regions"`
	Error            *string         `json:"error"`
}

// Validate checks the manifest invariants from docs/SCHEMA.md section 4.
// Times must be UTC, the run_id prefix must be started_at to the second, and
// slices must be non-nil so they serialize as JSON arrays, never null.
func (m RunManifest) Validate() error {
	var errs []error
	errs = append(errs, m.validateRunID()...)
	errs = append(errs, validateUTC("started_at", m.StartedAt, "finished_at", m.FinishedAt)...)
	if m.FinishedAt.Before(m.StartedAt) {
		errs = append(errs, errors.New("finished_at is before started_at"))
	}
	if !m.Status.Valid() {
		errs = append(errs, fmt.Errorf("status %q is not a RunStatus", m.Status))
	}
	if m.Status == RunStatusFailed && m.ObservationsPath != nil {
		errs = append(errs, errors.New("observations_path must be null when status is failed"))
	}
	if m.Status != RunStatusFailed && m.ObservationsPath == nil {
		errs = append(errs, errors.New("observations_path is required unless status is failed"))
	}
	if m.Sources == nil {
		errs = append(errs, errors.New("sources must be an array, not null"))
	}
	for i := range m.Sources {
		if err := m.Sources[i].Validate(); err != nil {
			errs = append(errs, fmt.Errorf("sources[%d]: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

func (m RunManifest) validateRunID() []error {
	if m.RunID == "" {
		return []error{errors.New("run_id is empty")}
	}
	t, err := RunIDTime(m.RunID)
	if err != nil {
		return []error{err}
	}
	if !t.Equal(m.StartedAt.Truncate(time.Second)) {
		return []error{fmt.Errorf("run_id prefix %s does not match started_at %s",
			t.Format(time.RFC3339), m.StartedAt.UTC().Format(time.RFC3339))}
	}
	return nil
}

// validateUTC reports every time in the (name, value) pairs whose location is
// not UTC. JSON encodes a time in its own location, and the contract requires
// a Z suffix.
func validateUTC(pairs ...any) []error {
	var errs []error
	for i := 0; i+1 < len(pairs); i += 2 {
		if t, ok := pairs[i+1].(time.Time); ok && t.Location() != time.UTC {
			errs = append(errs, fmt.Errorf("%s must be UTC", pairs[i]))
		}
	}
	return errs
}

// Validate checks one source entry.
func (s SourceRun) Validate() error {
	var errs []error
	if s.Source == "" {
		errs = append(errs, errors.New("source is empty"))
	}
	errs = append(errs, validateUTC("started_at", s.StartedAt, "finished_at", s.FinishedAt)...)
	if !s.Status.Valid() {
		errs = append(errs, fmt.Errorf("status %q is not a SourceRunStatus", s.Status))
	}
	if s.FinishedAt.Before(s.StartedAt) {
		errs = append(errs, errors.New("finished_at is before started_at"))
	}
	if s.Status == SourceRunFailed && s.Error == nil {
		errs = append(errs, errors.New("error is required when status is failed"))
	}
	if s.Status != SourceRunFailed && s.Error != nil {
		errs = append(errs, errors.New("error must be null unless status is failed"))
	}
	if s.Status == SourceRunFailed && s.RawPath != nil {
		errs = append(errs, errors.New("raw_path must be null when status is failed"))
	}
	if s.UnmappedRegions == nil {
		errs = append(errs, errors.New("unmapped_regions must be an array, not null"))
	}
	return errors.Join(errs...)
}
