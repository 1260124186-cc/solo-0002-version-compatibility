package repository

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxStateBytes = 64 << 20

func readState(path string) (*State, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return NewState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(data) > maxStateBytes {
		return nil, fmt.Errorf("state exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("state has trailing data")
	}
	normalizeState(&state)
	if err := validateState(&state); err != nil {
		return nil, fmt.Errorf("invalid state: %w", err)
	}
	return &state, nil
}

func writeState(path string, state *State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if len(data) > maxStateBytes {
		return fmt.Errorf("state exceeds size limit")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".compatibility-*.tmp")
	if err != nil {
		return fmt.Errorf("prepare state: %w", err)
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	// The rename is the commit point. Best-effort directory fsync must not
	// turn a committed update into a reported failure.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
