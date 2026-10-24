package schema

import (
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"time"
)

// runIDTimeLayout is the UTC prefix of a run id: <YYYYMMDD>T<HHMMSS>Z.
const runIDTimeLayout = "20060102T150405Z"

var runIDPattern = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{8}$`)

// NewRunID builds a run id from the run's UTC start time and eight lowercase
// hex characters read from rnd. The id sorts chronologically as a string and
// is safe to use in object keys.
func NewRunID(startedAt time.Time, rnd io.Reader) (string, error) {
	var b [4]byte
	if _, err := io.ReadFull(rnd, b[:]); err != nil {
		return "", fmt.Errorf("run id: random suffix: %w", err)
	}
	return startedAt.UTC().Format(runIDTimeLayout) + "-" + hex.EncodeToString(b[:]), nil
}

// RunIDTime returns the UTC start time encoded in a run id, or an error when
// the id does not match the documented format.
func RunIDTime(id string) (time.Time, error) {
	if !runIDPattern.MatchString(id) {
		return time.Time{}, fmt.Errorf("run id %q does not match <YYYYMMDD>T<HHMMSS>Z-<8 hex>", id)
	}
	t, err := time.Parse(runIDTimeLayout, id[:len(runIDTimeLayout)])
	if err != nil {
		return time.Time{}, fmt.Errorf("run id %q: %w", id, err)
	}
	return t, nil
}
