package store

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/parquet-go/parquet-go"

	"github.com/HMetcalfeW/gputick/internal/source"
	"github.com/HMetcalfeW/gputick/pkg/schema"
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
// upstream is the row key to read from a RawRow, empty for envelope columns.
type column struct {
	name     string
	upstream string
	index    int
	maxDef   int
}

// orderedGroup is a parquet.Group whose fields keep a given order. A plain
// Group is a map and sorts its fields by name, which would lose the upstream
// header order the raw file is meant to preserve. Only Fields is overridden,
// so rows must be written through WriteRows; GoType still reflects the map.
type orderedGroup struct {
	parquet.Group
	order []string
}

func (g orderedGroup) Fields() []parquet.Field {
	byName := make(map[string]parquet.Field, len(g.Group))
	for _, f := range g.Group.Fields() {
		byName[f.Name()] = f
	}
	out := make([]parquet.Field, 0, len(g.order))
	for _, n := range g.order {
		out = append(out, byName[n])
	}
	return out
}

// EncodeRaw validates raw and writes it as a raw snapshot: the five envelope
// columns followed by every upstream column, in header order, as optional
// strings passed through verbatim. The file's key-value metadata records each
// part and the header mapping. Every store backend writes raw objects with it.
func EncodeRaw(w io.Writer, runID, sourceName string, raw source.RawResult) error {
	if err := raw.Validate(); err != nil {
		return fmt.Errorf("invalid raw result: %w", err)
	}
	upstream := raw.Columns()
	names := payloadColumnNames(upstream)

	group := orderedGroup{Group: parquet.Group{
		colSource:          parquet.String(),
		colRunID:           parquet.String(),
		colFetchedAt:       parquet.Timestamp(parquet.Microsecond),
		colUpstreamVersion: parquet.Optional(parquet.String()),
		colUpstreamRef:     parquet.String(),
	}}
	group.order = append(group.order, envelopeColumns...)
	for _, n := range names {
		group.Group[n] = parquet.Optional(parquet.String())
		group.order = append(group.order, n)
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

	meta, err := json.Marshal(NewRawMetadata(runID, sourceName, raw, names))
	if err != nil {
		return fmt.Errorf("encode raw metadata: %w", err)
	}
	pw := parquet.NewGenericWriter[any](w, sch,
		parquet.Compression(&parquet.Zstd),
		parquet.KeyValueMetadata(RawMetadataKey, string(meta)))
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

// EncodeObservations validates obs for runID and writes them as Parquet.
func EncodeObservations(w io.Writer, runID string, obs []schema.Observation) error {
	if err := ValidateObservations(runID, obs); err != nil {
		return err
	}
	pw := parquet.NewGenericWriter[schema.Observation](w, parquet.Compression(&parquet.Zstd))
	if _, err := pw.Write(obs); err != nil {
		return fmt.Errorf("write observations: %w", err)
	}
	return pw.Close()
}

// EncodeManifest validates m and writes it as indented JSON.
func EncodeManifest(w io.Writer, m schema.RunManifest) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

func lookup(sch *parquet.Schema, name, upstream string) column {
	leaf, ok := sch.Lookup(name)
	if !ok {
		panic("store: raw schema is missing column " + name)
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

// payloadColumnNames maps normalized upstream columns to parquet column names.
// Columns are already non-empty and unique (source.NormalizeHeader); one that
// collides with an envelope column, or with a name already assigned, is
// prefixed with payload_ until it is free. The mapping is recorded in the file
// metadata so a renamed column is never ambiguous.
func payloadColumnNames(upstream []string) []string {
	taken := make(map[string]struct{}, len(envelopeColumns)+len(upstream))
	for _, n := range envelopeColumns {
		taken[n] = struct{}{}
	}
	names := make([]string, len(upstream))
	for i, h := range upstream {
		n := h
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
