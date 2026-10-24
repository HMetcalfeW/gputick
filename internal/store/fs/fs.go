// Package fs implements store.Store on a local directory. It is the store used
// in development and tests, and the reference for the object layout the R2
// store must reproduce. Writes go to a temporary file in the target directory
// and are renamed into place, so a crash never leaves a partial object at a
// final key. A single writer is assumed, which is true of the collector.
package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"

	"github.com/HMetcalfeW/gputick/internal/source"
	"github.com/HMetcalfeW/gputick/internal/store"
	"github.com/HMetcalfeW/gputick/pkg/schema"
)

type Store struct {
	root string
}

var _ store.Store = (*Store)(nil)

// New returns a store rooted at dir, creating it if needed.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("fs store: root directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("fs store: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("fs store: %w", err)
	}
	return &Store{root: abs}, nil
}

// Root is the absolute directory every key is relative to.
func (s *Store) Root() string { return s.root }

func (s *Store) WriteRaw(ctx context.Context, runID, sourceName string, raw source.RawResult) (string, error) {
	key, err := store.RawPath(sourceName, runID)
	if err != nil {
		return "", err
	}
	err = s.write(ctx, key, true, func(f *os.File) error {
		return writeRawParquet(f, runID, sourceName, raw)
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

func (s *Store) WriteObservations(ctx context.Context, runID string, obs []schema.Observation) (string, error) {
	key, err := store.ObservationsPath(runID)
	if err != nil {
		return "", err
	}
	err = s.write(ctx, key, false, func(f *os.File) error {
		w := parquet.NewGenericWriter[schema.Observation](f, parquet.Compression(&parquet.Zstd))
		if _, err := w.Write(obs); err != nil {
			return fmt.Errorf("write observations: %w", err)
		}
		return w.Close()
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

func (s *Store) WriteManifest(ctx context.Context, m schema.RunManifest) (string, error) {
	key, err := store.ManifestPath(m.RunID)
	if err != nil {
		return "", err
	}
	err = s.write(ctx, key, true, func(f *os.File) error {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// write streams fn into a temporary file beside the target and renames it into
// place. With once set, an existing target is an error and is left untouched.
func (s *Store) write(ctx context.Context, key string, once bool, fn func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	target := filepath.Join(s.root, filepath.FromSlash(key))
	if once {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("%s: %w", key, store.ErrExists)
		}
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	if err := fn(tmp); err != nil {
		return discard(tmp, key, err)
	}
	if err := tmp.Sync(); err != nil {
		return discard(tmp, key, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("%s: %w", key, err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func discard(tmp *os.File, key string, cause error) error {
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return fmt.Errorf("%s: %w", key, cause)
}
