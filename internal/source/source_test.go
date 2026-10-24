package source_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/HMetcalfeW/gputick/internal/source"
)

func TestHashBody(t *testing.T) {
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", source.HashBody(nil))
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", source.HashBody([]byte("hello")))
}

func TestUniqueColumns(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, source.UniqueColumns([]string{"a", "b", "c"}))
	assert.Equal(t, []string{"a", "b", "a_2", "a_2_2", "a_3", "", "_2"},
		source.UniqueColumns([]string{"a", "b", "a", "a_2", "a", "", ""}))
	assert.Empty(t, source.UniqueColumns(nil))
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
	at := time.Date(2026, 10, 24, 12, 0, 1, 0, time.UTC)
	r := source.RawResult{Parts: []source.Part{{
		Ref: "https://example.test/a.csv", UpstreamVersion: "v7", FetchedAt: at, Bytes: 10, SHA256: "aa",
		Columns: []string{"b", "a"}, Rows: []source.RawRow{{"a": "1", "b": "2"}, {"a": "3", "b": "4"}},
	}}}
	assert.Equal(t, 2, r.RowCount())
	assert.Equal(t, []string{"b", "a"}, r.Columns(), "upstream column order is preserved")
	assert.Equal(t, "aa", r.SHA256(), "a single part reports its own digest")
	assert.Equal(t, "v7", r.UpstreamVersion())
	assert.Equal(t, at, r.FetchedAt())
	assert.EqualValues(t, 10, r.Bytes())
}

func TestRawResult_MultiPart(t *testing.T) {
	t1 := time.Date(2026, 10, 24, 12, 0, 1, 0, time.UTC)
	t2 := t1.Add(5 * time.Second)
	r := source.RawResult{Parts: []source.Part{
		{Ref: "p1", UpstreamVersion: "v1", FetchedAt: t2, Bytes: 3, SHA256: "aa", Columns: []string{"a", "b"}, Rows: []source.RawRow{{"a": "1"}}},
		{Ref: "p2", UpstreamVersion: "v1", FetchedAt: t1, Bytes: 4, SHA256: "bb", Columns: []string{"b", "c"}, Rows: []source.RawRow{{"c": "1"}, {"c": "2"}}},
		{Ref: "p3", UpstreamVersion: "v2", FetchedAt: t1, Bytes: 5, SHA256: "cc", Columns: []string{"a"}},
		{Ref: "p4", FetchedAt: t1, Bytes: 0, SHA256: "dd", Columns: nil},
	}}
	assert.Equal(t, 3, r.RowCount())
	assert.Equal(t, []string{"a", "b", "c"}, r.Columns(), "union in first-seen order")
	assert.Equal(t, source.HashBody([]byte("aa\nbb\ncc\ndd")), r.SHA256())
	assert.Equal(t, "v1,v2", r.UpstreamVersion(), "distinct versions once each, empty skipped")
	assert.Equal(t, t2, r.FetchedAt(), "the latest part completion")
	assert.EqualValues(t, 12, r.Bytes())
}
