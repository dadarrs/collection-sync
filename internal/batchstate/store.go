package batchstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type fileState struct {
	Scopes map[string]scopeState `json:"scopes"`
}

type scopeState struct {
	Completed []string `json:"completed,omitempty"`
}

type Store struct {
	path string
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Completed(scope string) ([]string, error) {
	state, err := s.read()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), state.Scopes[scope].Completed...), nil
}

func (s *Store) SetCompleted(scope string, completed []string) error {
	state, err := s.read()
	if err != nil {
		return err
	}
	if state.Scopes == nil {
		state.Scopes = map[string]scopeState{}
	}
	state.Scopes[scope] = scopeState{Completed: append([]string(nil), completed...)}
	return s.write(state)
}

func (s *Store) ClearScope(scope string) error {
	state, err := s.read()
	if err != nil {
		return err
	}
	if len(state.Scopes) == 0 {
		return nil
	}
	delete(state.Scopes, scope)
	return s.write(state)
}

func (s *Store) read() (fileState, error) {
	if s == nil || s.path == "" {
		return fileState{}, errors.New("batch state path is required")
	}

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return fileState{Scopes: map[string]scopeState{}}, nil
	}
	if err != nil {
		return fileState{}, err
	}

	state := fileState{Scopes: map[string]scopeState{}}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return fileState{}, err
	}
	if state.Scopes == nil {
		state.Scopes = map[string]scopeState{}
	}
	return state, nil
}

func (s *Store) write(state fileState) error {
	if s == nil || s.path == "" {
		return errors.New("batch state path is required")
	}
	if state.Scopes == nil {
		state.Scopes = map[string]scopeState{}
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, s.path)
}
