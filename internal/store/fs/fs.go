// Package fs implements store.Store on a local directory. It is the store used
// in development and tests, and the reference for the object layout the R2
// store must reproduce. Encoding is shared with every backend through the
// store package; this package only publishes. Objects are built in tmp/ under
// the root and published by rename, or by hard link for write-once keys so a
// second writer fails instead of replacing the first. A crash never leaves a
// partial object at a final key.
package fs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

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
		return store.EncodeRaw(f, runID, sourceName, raw)
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
		return store.EncodeObservations(f, runID, obs)
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
		return store.EncodeManifest(f, m)
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// write streams fn into a temporary file under tmp/, then publishes it at
// key. Invalid input and cancellation leave nothing behind, not even the
// partition directory, because the target directory is created only after fn
// has succeeded. Once the object is published the write has happened, so the
// remaining housekeeping is logged rather than returned.
func (s *Store) write(ctx context.Context, key string, once bool, fn func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	tmpDir := filepath.Join(s.root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	tmp, err := os.CreateTemp(tmpDir, filepath.Base(key)+".*")
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
		return discard(tmp, key, err)
	}
	if err := ctx.Err(); err != nil {
		return discard(tmp, key, err)
	}
	target := filepath.Join(s.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return discard(tmp, key, err)
	}
	if err := publish(tmp.Name(), target, once); err != nil {
		return discard(tmp, key, err)
	}
	syncDirs(s.root, filepath.Dir(target))
	return nil
}

// publish moves tmp to target. A write-once key uses a hard link, which fails
// atomically when the target exists, where a rename would replace it. After a
// successful link the temp file is removed on a best-effort basis.
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
	if err := os.Remove(tmp); err != nil {
		slog.Warn("published object but could not remove its temp file",
			"func", "fs.publish", "tmp", tmp, "err", err)
	}
	return nil
}

// syncDirs fsyncs dir and each ancestor up to root so newly created partition
// directories are durable. The object is already published, so a failure is
// logged, not returned: reporting it would make a write that happened look
// failed and leave a write-once key that can never be retried.
func syncDirs(root, dir string) {
	for d := dir; ; d = filepath.Dir(d) {
		if err := syncDir(d); err != nil {
			slog.Warn("directory sync failed after publish", "func", "fs.syncDirs", "dir", d, "err", err)
		}
		if d == root || filepath.Dir(d) == d {
			return
		}
	}
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// discard closes and removes the temp file; the close error is irrelevant
// because the file is being thrown away.
func discard(tmp *os.File, key string, cause error) error {
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return fmt.Errorf("%s: %w", key, cause)
}
