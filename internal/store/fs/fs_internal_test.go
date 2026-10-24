package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HMetcalfeW/gputick/internal/store"
)

const key = "manifest/date=2026-10-24/20261024T120000Z-3f9a1c2e.json"

func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, filepath.Base(path))
		}
		return err
	})
	require.NoError(t, err)
	return out
}

func writeString(content string) func(*os.File) error {
	return func(f *os.File) error {
		_, err := f.WriteString(content)
		return err
	}
}

func TestWrite_FailureInsideFnLeavesNothing(t *testing.T) {
	s, err := New(t.TempDir())
	require.NoError(t, err)
	err = s.write(context.Background(), key, true, func(*os.File) error { return errors.New("boom") })
	require.ErrorContains(t, err, "boom")
	assert.Empty(t, files(t, s.root))
	_, err = os.Stat(filepath.Join(s.root, "manifest"))
	assert.ErrorIs(t, err, os.ErrNotExist, "the partition directory is created only after fn succeeds")
}

func TestWrite_CancelDuringFnLeavesNothing(t *testing.T) {
	s, err := New(t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	err = s.write(ctx, key, false, func(f *os.File) error {
		cancel()
		return writeString("partial")(f)
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, files(t, s.root))
}

func TestWrite_OnceIsAtomicUnderConcurrency(t *testing.T) {
	s, err := New(t.TempDir())
	require.NoError(t, err)
	const writers = 8
	// Every writer finishes building its object before any of them publishes,
	// so the publish calls genuinely overlap instead of running one after another.
	var ready sync.WaitGroup
	ready.Add(writers)
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.write(context.Background(), key, true, func(f *os.File) error {
				_, err := fmt.Fprintf(f, "writer-%d", i)
				ready.Done()
				ready.Wait()
				return err
			})
		}()
	}
	wg.Wait()
	close(results)

	wins, exists := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, store.ErrExists):
			exists++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, wins, "exactly one writer publishes")
	assert.Equal(t, writers-1, exists, "every other writer sees ErrExists")
	assert.Equal(t, []string{filepath.Base(key)}, files(t, s.root), "no temp files survive")
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(key)))
	require.NoError(t, err)
	assert.Regexp(t, `^writer-\d$`, string(data), "the published content is one whole write")
}

func TestWrite_OverwriteReplaces(t *testing.T) {
	s, err := New(t.TempDir())
	require.NoError(t, err)
	obs := "obs/date=2026-10-24/20261024T120000Z-3f9a1c2e.parquet"
	for _, content := range []string{"first", "second"} {
		require.NoError(t, s.write(context.Background(), obs, false, writeString(content)))
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(obs)))
	require.NoError(t, err)
	assert.Equal(t, "second", string(data))
	assert.Len(t, files(t, s.root), 1)
}

func TestWrite_PublishSucceedsWhenDirectorySyncFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s, err := New(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(s.root, filepath.FromSlash(filepath.Dir(key)))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	// Write and search only: linking into the directory works, opening it to
	// fsync does not. The object is published, so the write must succeed.
	require.NoError(t, os.Chmod(dir, 0o300))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	require.NoError(t, s.write(context.Background(), key, true, writeString("x")))
	_, err = os.Lstat(filepath.Join(s.root, filepath.FromSlash(key)))
	require.NoError(t, err, "the object exists")
	err = s.write(context.Background(), key, true, writeString("y"))
	assert.ErrorIs(t, err, store.ErrExists, "and the key is correctly reported as taken afterwards")
}
