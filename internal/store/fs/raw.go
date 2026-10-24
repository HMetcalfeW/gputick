package fs

import (
	"fmt"
	"io"
	"slices"

	"github.com/parquet-go/parquet-go"

	"github.com/HMetcalfeW/gputick/internal/source"
)

// Envelope column names, in the order documented in docs/SCHEMA.md section 2.
const (
	colSource          = "source"
	colRunID           = "run_id"
	colFetchedAt       = "fetched_at"
	colUpstreamVersion = "upstream_version"
	colUpstreamRef     = "upstream_ref"
)

var envelopeColumns = []string{colSource, colRunID, colFetchedAt, colUpstreamVersion, colUpstreamRef}

// column is one leaf of the raw schema with the parquet position it landed at.
// upstream is the header name to read from a RawRow, empty for envelope columns.
type column struct {
	name     string
	upstream string
	index    int
	maxDef   int
}

// writeRawParquet writes the envelope columns followed by every upstream
// column as an optional string. Upstream values are passed through verbatim.
func writeRawParquet(w io.Writer, runID, sourceName string, raw source.RawResult) error {
	upstream := raw.Columns()
	names := payloadColumnNames(upstream)

	group := parquet.Group{
		colSource:          parquet.String(),
		colRunID:           parquet.String(),
		colFetchedAt:       parquet.Timestamp(parquet.Microsecond),
		colUpstreamVersion: parquet.Optional(parquet.String()),
		colUpstreamRef:     parquet.String(),
	}
	for _, n := range names {
		group[n] = parquet.Optional(parquet.String())
	}
	sch := parquet.NewSchema("raw", group)

	cols := make([]column, 0, len(envelopeColumns)+len(names))
	for _, n := range envelopeColumns {
		cols = append(cols, lookup(sch, n, ""))
	}
	for i, n := range names {
		cols = append(cols, lookup(sch, n, upstream[i]))
	}
	// Rows must list values in column index order.
	slices.SortFunc(cols, func(a, b column) int { return a.index - b.index })

	pw := parquet.NewGenericWriter[any](w, sch, parquet.Compression(&parquet.Zstd))
	for _, part := range raw.Parts {
		rows := make([]parquet.Row, 0, len(part.Rows))
		for _, r := range part.Rows {
			rows = append(rows, buildRow(cols, runID, sourceName, part, r))
		}
		if len(rows) == 0 {
			continue
		}
		if _, err := pw.WriteRows(rows); err != nil {
			return fmt.Errorf("write raw rows for %s: %w", part.Ref, err)
		}
	}
	return pw.Close()
}

func lookup(sch *parquet.Schema, name, upstream string) column {
	leaf, ok := sch.Lookup(name)
	if !ok {
		panic("fs: raw schema is missing column " + name)
	}
	return column{name: name, upstream: upstream, index: leaf.ColumnIndex, maxDef: leaf.MaxDefinitionLevel}
}

func buildRow(cols []column, runID, sourceName string, part source.Part, r source.RawRow) parquet.Row {
	row := make(parquet.Row, 0, len(cols))
	for _, c := range cols {
		switch c.name {
		case colSource:
			row = append(row, parquet.ValueOf(sourceName).Level(0, c.maxDef, c.index))
		case colRunID:
			row = append(row, parquet.ValueOf(runID).Level(0, c.maxDef, c.index))
		case colFetchedAt:
			row = append(row, parquet.ValueOf(part.FetchedAt.UnixMicro()).Level(0, c.maxDef, c.index))
		case colUpstreamVersion:
			row = append(row, optionalString(part.UpstreamVersion != "", part.UpstreamVersion, c))
		case colUpstreamRef:
			row = append(row, parquet.ValueOf(part.Ref).Level(0, c.maxDef, c.index))
		default:
			v, ok := r[c.upstream]
			row = append(row, optionalString(ok, v, c))
		}
	}
	return row
}

func optionalString(present bool, v string, c column) parquet.Value {
	if !present {
		return parquet.NullValue().Level(0, 0, c.index)
	}
	return parquet.ValueOf(v).Level(0, c.maxDef, c.index)
}

// payloadColumnNames maps upstream headers to parquet column names. A header
// that collides with an envelope column, or with a name already assigned, is
// prefixed with payload_ until it is free. An empty header is named by its
// position. Repeated headers never reach here: RawResult.Columns is a set, and
// adapters rename repeats with source.UniqueColumns before building rows.
func payloadColumnNames(upstream []string) []string {
	taken := make(map[string]struct{}, len(envelopeColumns)+len(upstream))
	for _, n := range envelopeColumns {
		taken[n] = struct{}{}
	}
	names := make([]string, len(upstream))
	for i, h := range upstream {
		n := h
		if n == "" {
			n = fmt.Sprintf("unnamed_%d", i)
		}
		for {
			if _, clash := taken[n]; !clash {
				break
			}
			n = "payload_" + n
		}
		taken[n] = struct{}{}
		names[i] = n
	}
	return names
}
