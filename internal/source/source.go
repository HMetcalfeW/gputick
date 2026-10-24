// Package source defines what a gputick data source returns. A Source fetches
// one upstream and hands back its rows untyped, exactly as published, so the
// raw snapshot can be written before any interpretation happens.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/HMetcalfeW/gputick/pkg/schema"
)

// RawRow is one upstream record with values kept as strings. Typing happens
// in the normalizer, never here.
type RawRow map[string]string

// Part is one table fetched from an upstream: a CSV file, one member of an
// archive, or one endpoint's response. A source that reads several tables
// returns one Part per table. Bytes and SHA256 describe the table body as the
// adapter received it (for an archive member, that member's uncompressed
// bytes; for an SDK response with no body, its canonical JSON encoding).
type Part struct {
	// Ref identifies the table: a URL, an S3 key, either followed by
	// "#<member>" for a file inside an archive, or an API operation name,
	// always with credentials removed. It becomes the envelope's upstream_ref.
	Ref string
	// UpstreamVersion is the upstream's own version string, empty when none.
	UpstreamVersion string
	// FetchedAt is when the request that carried the table completed. It must
	// have a zero UTC offset; the raw column keeps microsecond precision.
	FetchedAt time.Time
	Bytes     int64
	// SHA256 is the lowercase hex digest of the table body.
	SHA256 string
	// Header is the table header exactly as received, in upstream order,
	// including empty and repeated names. Columns derives the row keys from it.
	Header []string
	// Rows are keyed by Columns, never by Header.
	Rows []RawRow
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
	// It is all-or-nothing: on error the result is discarded and the source is
	// recorded as failed for the run, so an implementation must never return
	// partial parts with a nil error. Implementations honor ctx cancellation
	// and do not retry forever.
	Fetch(ctx context.Context) (RawResult, error)
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// NormalizeHeader makes a table header usable as RawRow keys: an empty name
// becomes unnamed_<i> from its zero-based position, and a repeated name gets
// a _<n> suffix from its second occurrence on. The first occurrence keeps its
// name. Part.Columns applies it, and adapters key their rows by the result.
func NormalizeHeader(header []string) []string {
	out := make([]string, len(header))
	taken := make(map[string]struct{}, len(header))
	for i, h := range header {
		base := h
		if base == "" {
			base = fmt.Sprintf("unnamed_%d", i)
		}
		n := base
		for k := 2; ; k++ {
			if _, dup := taken[n]; !dup {
				break
			}
			n = fmt.Sprintf("%s_%d", base, k)
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

// Columns returns the row keys for this table: NormalizeHeader(Header).
func (p Part) Columns() []string { return NormalizeHeader(p.Header) }

// Validate checks that the part can be written as a raw snapshot without
// losing anything: required metadata is present and well formed, and every
// row key is a column.
func (p Part) Validate() error {
	var errs []error
	if p.Ref == "" {
		errs = append(errs, errors.New("ref is empty"))
	}
	if p.FetchedAt.IsZero() {
		errs = append(errs, errors.New("fetched_at is zero"))
	} else if err := schema.CheckUTC("fetched_at", p.FetchedAt); err != nil {
		errs = append(errs, err)
	}
	if p.Bytes < 0 {
		errs = append(errs, errors.New("bytes is negative"))
	}
	if !sha256Pattern.MatchString(p.SHA256) {
		errs = append(errs, errors.New("sha256 is not 64 lowercase hex characters"))
	}
	if err := unknownKey(p.Rows, p.Columns()); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// unknownKey reports the first row with a key that is not a column, naming
// its alphabetically first unknown key so the message is deterministic. One
// error is enough: a whole column of them would otherwise flood the message.
func unknownKey(rows []RawRow, columns []string) error {
	cols := make(map[string]struct{}, len(columns))
	for _, c := range columns {
		cols[c] = struct{}{}
	}
	for i, r := range rows {
		var unknown []string
		for k := range r {
			if _, ok := cols[k]; !ok {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			slices.Sort(unknown)
			return fmt.Errorf("row %d has key %q that is not in columns", i, unknown[0])
		}
	}
	return nil
}

// Validate validates every part, naming the part in each error.
func (r RawResult) Validate() error {
	var errs []error
	for i, p := range r.Parts {
		if err := p.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("part %d (%s): %w", i, p.Ref, err))
		}
	}
	return errors.Join(errs...)
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
		for _, c := range p.Columns() {
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
// is one part, otherwise the digest of the part digests joined with newlines
// in part order. Per-part digests are kept in the raw file's metadata.
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
