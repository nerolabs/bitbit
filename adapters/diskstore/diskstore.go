// Package diskstore is a filesystem ChunkStore laid out content-addressed:
//
//	<root>/objects/<first two hex chars>/<full hex hash>
//
// The two-char fan-out keeps directories a sane size. Writes go to a
// temp file then rename, so a crash never leaves a half-written chunk
// under a valid name. Reads re-verify the hash — disks lie too.
package diskstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"shardnet/ports"
)

type Store struct {
	root string
}

var _ ports.ChunkStore = (*Store)(nil)

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "objects"), 0o755); err != nil {
		return nil, fmt.Errorf("diskstore: %w", err)
	}
	return &Store{root: root}, nil
}

func (s *Store) path(id ports.ChunkID) string {
	hex := id.String()
	return filepath.Join(s.root, "objects", hex[:2], hex)
}

func (s *Store) Put(_ context.Context, c ports.Chunk) error {
	if !c.Verify() {
		return fmt.Errorf("diskstore put %s: %w", c.ID, ports.ErrCorrupt)
	}
	dst := s.path(c.ID)
	if _, err := os.Stat(dst); err == nil {
		return nil // content-addressed: same name ⇒ same bytes
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(c.Data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func (s *Store) Get(_ context.Context, id ports.ChunkID) (ports.Chunk, error) {
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return ports.Chunk{}, fmt.Errorf("diskstore get %s: %w", id, ports.ErrNotFound)
	}
	if err != nil {
		return ports.Chunk{}, err
	}
	c := ports.Chunk{ID: id, Data: data}
	if !c.Verify() {
		return ports.Chunk{}, fmt.Errorf("diskstore get %s: %w", id, ports.ErrCorrupt)
	}
	return c, nil
}

func (s *Store) Has(_ context.Context, id ports.ChunkID) (bool, error) {
	_, err := os.Stat(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) List(_ context.Context) ([]ports.ChunkID, error) {
	var ids []ports.ChunkID
	objects := filepath.Join(s.root, "objects")
	err := filepath.WalkDir(objects, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		id, perr := ports.ParseHash(d.Name())
		if perr != nil {
			return nil // skip temp files and strays
		}
		ids = append(ids, id)
		return nil
	})
	return ids, err
}

func (s *Store) Delete(_ context.Context, id ports.ChunkID) error {
	err := os.Remove(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
