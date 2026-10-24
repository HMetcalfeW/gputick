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
// Slices must be non-nil so they serialize as JSON arrays, never null.
func (m RunManifest) Validate() error {
	var errs []error
	if m.RunID == "" {
		errs = append(errs, errors.New("run_id is empty"))
	} else if _, err := RunIDTime(m.RunID); err != nil {
		errs = append(errs, err)
	}
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

// Validate checks one source entry.
func (s SourceRun) Validate() error {
	var errs []error
	if s.Source == "" {
		errs = append(errs, errors.New("source is empty"))
	}
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
