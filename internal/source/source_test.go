package source_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/internal/source"
)

const sha = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

func TestHashBody(t *testing.T) {
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", source.HashBody(nil))
	assert.Equal(t, sha, source.HashBody([]byte("hello")))
}

func TestNormalizeHeader(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"unique", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"repeats", []string{"a", "b", "a", "a_2", "a"}, []string{"a", "b", "a_2", "a_2_2", "a_3"}},
		{"empty by position", []string{"x", "", ""}, []string{"x", "unnamed_1", "unnamed_2"}},
		{"empty colliding with literal", []string{"", "unnamed_0"}, []string{"unnamed_0", "unnamed_0_2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, source.NormalizeHeader(tt.in))
		})
	}
}

func validPart() source.Part {
	return source.Part{
		Ref: "https://example.test/a.csv", UpstreamVersion: "v7",
		FetchedAt: time.Date(2026, 10, 24, 12, 0, 1, 0, time.UTC), Bytes: 10, SHA256: sha,
		Header: []string{"b", "a"}, Rows: []source.RawRow{{"a": "1", "b": "2"}, {"a": "3"}},
	}
}

func TestPart_Columns(t *testing.T) {
	p := source.Part{Header: []string{"a", "", "a"}}
	assert.Equal(t, []string{"a", "unnamed_1", "a_2"}, p.Columns())
	assert.Empty(t, source.Part{}.Columns())
}

func TestPart_Validate(t *testing.T) {
	require.NoError(t, validPart().Validate())
	empty := source.Part{Ref: "r", FetchedAt: time.Now().UTC(), SHA256: sha}
	require.NoError(t, empty.Validate(), "a table with no columns and no rows is valid")

	tests := []struct {
		name    string
		mutate  func(p *source.Part)
		wantErr string
	}{
		{"empty ref", func(p *source.Part) { p.Ref = "" }, "ref is empty"},
		{"zero fetched_at", func(p *source.Part) { p.FetchedAt = time.Time{} }, "fetched_at is zero"},
		{"local fetched_at", func(p *source.Part) { p.FetchedAt = p.FetchedAt.In(time.FixedZone("EDT", -4*3600)) }, "fetched_at must be UTC"},
		{"negative bytes", func(p *source.Part) { p.Bytes = -1 }, "bytes is negative"},
		{"empty sha", func(p *source.Part) { p.SHA256 = "" }, "sha256 is not 64 lowercase hex"},
		{"uppercase sha", func(p *source.Part) { p.SHA256 = "2CF24DBA5FB0A30E26E83B2AC5B9E29E1B161E5C1FA7425E73043362938B9824" }, "sha256 is not"},
		{"row key outside columns", func(p *source.Part) { p.Rows[1]["zzz"] = "lost" }, `row 1 has key "zzz"`},
		{"row keyed by header not columns", func(p *source.Part) {
			p.Header = []string{"a", ""}
			p.Rows = []source.RawRow{{"a": "1", "": "blank"}}
		}, `row 0 has key "" that is not in columns`},
		{"deterministic first unknown key", func(p *source.Part) { p.Rows[0]["zz"], p.Rows[0]["aa"] = "1", "2" }, `row 0 has key "aa"`},
		{"rows without header", func(p *source.Part) { p.Header = nil }, "is not in columns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPart()
			tt.mutate(&p)
			err := p.Validate()
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestRawResult_Validate(t *testing.T) {
	good := validPart()
	bad := validPart()
	bad.Ref = ""
	bad.SHA256 = "nope"
	err := source.RawResult{Parts: []source.Part{good, bad}}.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "part 1 (): ref is empty")
	require.ErrorContains(t, err, "sha256 is not")
	assert.NoError(t, source.RawResult{}.Validate())
}

func TestRawResult_Empty(t *testing.T) {
	var r source.RawResult
	assert.Equal(t, 0, r.RowCount())
	assert.Empty(t, r.Columns())
	assert.Empty(t, r.SHA256())
	assert.Empty(t, r.UpstreamVersion())
	assert.True(t, r.FetchedAt().IsZero())
	assert.Zero(t, r.Bytes())
}

func TestRawResult_SinglePart(t *testing.T) {
	p := validPart()
	r := source.RawResult{Parts: []source.Part{p}}
	assert.Equal(t, 2, r.RowCount())
	assert.Equal(t, []string{"b", "a"}, r.Columns(), "upstream column order is preserved")
	assert.Equal(t, sha, r.SHA256(), "a single part reports its own digest")
	assert.Equal(t, "v7", r.UpstreamVersion())
	assert.Equal(t, p.FetchedAt, r.FetchedAt())
	assert.EqualValues(t, 10, r.Bytes())
}

func TestRawResult_MultiPart(t *testing.T) {
	t1 := time.Date(2026, 10, 24, 12, 0, 1, 0, time.UTC)
	t2 := t1.Add(5 * time.Second)
	r := source.RawResult{Parts: []source.Part{
		{Ref: "p1", UpstreamVersion: "v1", FetchedAt: t2, Bytes: 3, SHA256: "aa", Header: []string{"a", "b"}, Rows: []source.RawRow{{"a": "1"}}},
		{Ref: "p2", UpstreamVersion: "v1", FetchedAt: t1, Bytes: 4, SHA256: "bb", Header: []string{"b", "c"}, Rows: []source.RawRow{{"c": "1"}, {"c": "2"}}},
		{Ref: "p3", UpstreamVersion: "v2", FetchedAt: t1, Bytes: 5, SHA256: "cc", Header: []string{"a"}},
		{Ref: "p4", FetchedAt: t1, Bytes: 0, SHA256: "dd", Header: nil},
	}}
	assert.Equal(t, 3, r.RowCount())
	assert.Equal(t, []string{"a", "b", "c"}, r.Columns(), "union in first-seen order")
	assert.Equal(t, source.HashBody([]byte("aa\nbb\ncc\ndd")), r.SHA256(), "digest of the part digests, in order")
	assert.Equal(t, "v1,v2", r.UpstreamVersion(), "distinct versions once each, empty skipped")
	assert.Equal(t, t2, r.FetchedAt(), "the latest part completion")
	assert.EqualValues(t, 12, r.Bytes())
}
