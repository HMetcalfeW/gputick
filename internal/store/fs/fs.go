// Package fs implements store.Store on a local directory. It is the store used
// in development and tests, and the reference for the object layout the R2
// store must reproduce. Writes go to a temporary file in the target directory
// and are published by rename, or by hard link for write-once keys so that a
// second writer fails instead of replacing the first. A crash never leaves a
// partial object at a final key.
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
	if err := raw.Validate(); err != nil {
		return "", fmt.Errorf("%s: invalid raw result: %w", key, err)
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
	if err := store.ValidateObservations(runID, obs); err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
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
	if err := m.Validate(); err != nil {
		return "", fmt.Errorf("%s: invalid manifest: %w", key, err)
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

// write streams fn into a temporary file beside the target, then publishes it.
// The context is checked again before publishing so a cancellation during fn
// leaves no object behind.
func (s *Store) write(ctx context.Context, key string, once bool, fn func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	target := filepath.Join(s.root, filepath.FromSlash(key))
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
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("%s: %w", key, err)
	}
	if err := publish(tmp.Name(), target, once); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("%s: %w", key, err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

// publish moves tmp to target. A write-once key uses a hard link, which fails
// atomically when the target exists, where a rename would replace it.
func publish(tmp, target string, once bool) error {
	if !once {
		return os.Rename(tmp, target)
	}
	if err := os.Link(tmp, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return store.ErrExists
		}
		return err
	}
	return os.Remove(tmp)
}

// syncDir makes the directory entry durable, so a manifest that survives a
// crash never points at a raw file whose rename did not.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func discard(tmp *os.File, key string, cause error) error {
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return fmt.Errorf("%s: %w", key, cause)
}
