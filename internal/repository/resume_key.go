package repository

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const resumeKeyName = ".resume.key"

// ResumeKey returns the process-independent secret used to authenticate
// continuation tokens. It is stored outside state.json so catalog revisions and
// business events remain unchanged.
func (r *Repository) ResumeKey() ([]byte, error) {
	path := filepath.Join(filepath.Dir(r.path), resumeKeyName)
	data, err := os.ReadFile(path)
	if err == nil {
		return decodeResumeKey(data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read continuation key: %w", err)
	}

	var raw [32]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return nil, err
	}
	encoded := make([]byte, base64.RawStdEncoding.EncodedLen(len(raw)))
	base64.RawStdEncoding.Encode(encoded, raw[:])

	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".resume-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("prepare continuation key: %w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write continuation key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync continuation key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close continuation key: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return nil, fmt.Errorf("replace continuation key: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return raw[:], nil
}

func decodeResumeKey(data []byte) ([]byte, error) {
	key := make([]byte, 32)
	n, err := base64.RawStdEncoding.Decode(key, data)
	if err != nil || n != len(key) {
		return nil, fmt.Errorf("invalid continuation key")
	}
	return key, nil
}
