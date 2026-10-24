// Package source defines what a gputick data source returns. A Source fetches
// one upstream and hands back its rows untyped, exactly as published, so the
// raw snapshot can be written before any interpretation happens.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// RawRow is one upstream record with values kept as strings. Typing happens
// in the normalizer, never here.
type RawRow map[string]string

// Part is the result of one upstream request. A source that reads several
// files or endpoints returns one Part per request so the raw envelope can
// record where each row came from.
type Part struct {
	// Ref identifies the request: a URL, an S3 key or an API operation name,
	// with credentials removed. It becomes the raw envelope's upstream_ref.
	Ref string
	// UpstreamVersion is the upstream's own version string, empty when none.
	UpstreamVersion string
	// FetchedAt is when the request completed.
	FetchedAt time.Time
	// Bytes is the size of the fetched body.
	Bytes int64
	// SHA256 is the lowercase hex digest of the fetched body.
	SHA256 string
	// Columns is the upstream header in upstream order. Names must be unique
	// because they are RawRow keys; see UniqueColumns.
	Columns []string
	Rows    []RawRow
}

// RawResult is everything a Source fetched in one run.
type RawResult struct {
	Parts []Part
}

// Source is implemented once per upstream under internal/source/<name>.
type Source interface {
	// Name is the source slug used in paths, manifests and observations.
	Name() string
	// Fetch performs every request the source needs and returns the raw rows.
	// Implementations must honor ctx cancellation and must not retry forever.
	Fetch(ctx context.Context) (RawResult, error)
}

// UniqueColumns renames repeated header names with a _<n> suffix from the
// second occurrence on, so every column can be a RawRow key. The first
// occurrence keeps its name.
func UniqueColumns(header []string) []string {
	out := make([]string, len(header))
	taken := make(map[string]struct{}, len(header))
	for i, h := range header {
		n := h
		for k := 2; ; k++ {
			if _, dup := taken[n]; !dup {
				break
			}
			n = fmt.Sprintf("%s_%d", h, k)
		}
		taken[n] = struct{}{}
		out[i] = n
	}
	return out
}

// HashBody returns the lowercase hex SHA-256 of a fetched body.
func HashBody(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// RowCount is the total number of rows across all parts.
func (r RawResult) RowCount() int {
	n := 0
	for _, p := range r.Parts {
		n += len(p.Rows)
	}
	return n
}

// Columns is the union of all part columns in first-seen order.
func (r RawResult) Columns() []string {
	var cols []string
	seen := map[string]struct{}{}
	for _, p := range r.Parts {
		for _, c := range p.Columns {
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			cols = append(cols, c)
		}
	}
	return cols
}

// SHA256 is a single digest for the whole fetch: the part digest when there
// is one part, otherwise the digest of the part digests joined in order.
func (r RawResult) SHA256() string {
	switch len(r.Parts) {
	case 0:
		return ""
	case 1:
		return r.Parts[0].SHA256
	}
	hashes := make([]string, len(r.Parts))
	for i, p := range r.Parts {
		hashes[i] = p.SHA256
	}
	return HashBody([]byte(strings.Join(hashes, "\n")))
}

// UpstreamVersion is the distinct part versions joined with "," in part
// order, so a source whose parts share one version reports it once.
func (r RawResult) UpstreamVersion() string {
	var versions []string
	seen := map[string]struct{}{}
	for _, p := range r.Parts {
		if p.UpstreamVersion == "" {
			continue
		}
		if _, ok := seen[p.UpstreamVersion]; ok {
			continue
		}
		seen[p.UpstreamVersion] = struct{}{}
		versions = append(versions, p.UpstreamVersion)
	}
	return strings.Join(versions, ",")
}

// FetchedAt is when the last part completed, which is when the fetch as a
// whole completed.
func (r RawResult) FetchedAt() time.Time {
	var latest time.Time
	for _, p := range r.Parts {
		if p.FetchedAt.After(latest) {
			latest = p.FetchedAt
		}
	}
	return latest
}

// Bytes is the total fetched body size across parts.
func (r RawResult) Bytes() int64 {
	var n int64
	for _, p := range r.Parts {
		n += p.Bytes
	}
	return n
}
