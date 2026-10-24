package store

import (
	"time"

	"github.com/HMetcalfeW/gputick/internal/source"
)

// RawMetadataKey is the Parquet key-value metadata entry that holds a
// RawMetadata JSON document in every raw file.
const RawMetadataKey = "gputick.raw"

// RawMetadata describes a raw file well enough to audit it without the
// manifest: where each part came from, what its header was exactly, and how
// normalized column names map to Parquet column names.
type RawMetadata struct {
	Source  string      `json:"source"`
	RunID   string      `json:"run_id"`
	Columns []RawColumn `json:"columns"`
	Parts   []RawPart   `json:"parts"`
}

// RawColumn maps one normalized upstream column to the Parquet column it became.
type RawColumn struct {
	Upstream string `json:"upstream"`
	Column   string `json:"column"`
}

// RawPart is the fetch metadata of one part. Header is the table header as
// received, Columns is its normalized form used as row keys, and FetchedAt is
// truncated to the microsecond precision of the fetched_at column.
type RawPart struct {
	Ref             string    `json:"ref"`
	UpstreamVersion string    `json:"upstream_version"`
	FetchedAt       time.Time `json:"fetched_at"`
	Bytes           int64     `json:"bytes"`
	SHA256          string    `json:"sha256"`
	Header          []string  `json:"header"`
	Columns         []string  `json:"columns"`
	Rows            int       `json:"rows"`
}

// NewRawMetadata builds the metadata for a raw file. columnNames holds the
// Parquet name for each entry of raw.Columns(), in the same order.
func NewRawMetadata(runID, sourceName string, raw source.RawResult, columnNames []string) RawMetadata {
	upstream := raw.Columns()
	m := RawMetadata{
		Source:  sourceName,
		RunID:   runID,
		Columns: make([]RawColumn, len(upstream)),
		Parts:   make([]RawPart, len(raw.Parts)),
	}
	for i, u := range upstream {
		m.Columns[i] = RawColumn{Upstream: u, Column: columnNames[i]}
	}
	for i, p := range raw.Parts {
		m.Parts[i] = RawPart{
			Ref: p.Ref, UpstreamVersion: p.UpstreamVersion,
			FetchedAt: p.FetchedAt.UTC().Truncate(time.Microsecond),
			Bytes:     p.Bytes, SHA256: p.SHA256,
			Header: nonNil(p.Header), Columns: nonNil(p.Columns()), Rows: len(p.Rows),
		}
	}
	return m
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
