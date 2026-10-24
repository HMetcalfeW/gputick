package schema_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/pkg/schema"
)

func TestNewRunID(t *testing.T) {
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.FixedZone("EDT", -4*3600))
	id, err := schema.NewRunID(start, bytes.NewReader([]byte{0x3f, 0x9a, 0x1c, 0x2e}))
	require.NoError(t, err)
	assert.Equal(t, "20261024T160000Z-3f9a1c2e", id, "local time is converted to UTC")

	got, err := schema.RunIDTime(id)
	require.NoError(t, err)
	assert.Equal(t, start.UTC(), got)
}

func TestNewRunID_ShortRandomSource(t *testing.T) {
	_, err := schema.NewRunID(time.Now(), strings.NewReader("ab"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "random suffix")
}

func TestRunIDTime_RejectsMalformed(t *testing.T) {
	for _, id := range []string{
		"",
		"20261024T120000Z",
		"20261024T120000Z-3F9A1C2E",
		"20261024T120000Z-3f9a1c2",
		"20261024-120000Z-3f9a1c2e",
		"2026-10-24T12:00:00Z-3f9a1c2e",
		"20261324T120000Z-3f9a1c2e",
	} {
		t.Run(id, func(t *testing.T) {
			_, err := schema.RunIDTime(id)
			assert.Error(t, err)
		})
	}
}

func TestRunID_SortsChronologically(t *testing.T) {
	rnd := bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00})
	earlier, err := schema.NewRunID(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), rnd)
	require.NoError(t, err)
	later, err := schema.NewRunID(time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC), rnd)
	require.NoError(t, err)
	assert.Less(t, earlier, later)
}
