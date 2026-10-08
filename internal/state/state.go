// Package state keeps drillbook's local records under the state directory:
//
//	results.jsonl         one JSON line per finished drill
//	active/<id>.json      drills whose fault may still be applied
//	control-cache.json    control-run outcomes keyed by drill file hash
//	logs/<id>.log         full output of each drill
//	snapshots/            Deployment snapshots for kube faults
package state

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Store struct{ Dir string }

// Active records a drill in progress, so `drillbook abort` can undo it.
type Active struct {
	ID        string    `json:"id"`
	DrillFile string    `json:"drill_file"`
	SilenceID string    `json:"silence_id,omitempty"`
	Started   time.Time `json:"started"`
}

func (s *Store) SnapshotDir() string { return filepath.Join(s.Dir, "snapshots") }

func (s *Store) MarkActive(a Active) error {
	return writeJSON(filepath.Join(s.Dir, "active", a.ID+".json"), a)
}

func (s *Store) ClearActive(id string) error {
	err := os.Remove(filepath.Join(s.Dir, "active", id+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) Active() ([]Active, error) {
	paths, err := filepath.Glob(filepath.Join(s.Dir, "active", "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Active
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var a Active
		if err := json.Unmarshal(b, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) AppendResult(r any) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "results.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *Store) controlPath() string { return filepath.Join(s.Dir, "control-cache.json") }

func (s *Store) loadControl() map[string]bool {
	m := map[string]bool{}
	if b, err := os.ReadFile(s.controlPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// Control returns whether the control run for this drill hash cleared on its own.
func (s *Store) Control(hash string) (cleared, ok bool) {
	cleared, ok = s.loadControl()[hash]
	return
}

func (s *Store) SaveControl(hash string, cleared bool) error {
	m := s.loadControl()
	m[hash] = cleared
	return writeJSON(s.controlPath(), m)
}

func (s *Store) LogFile(id string) (io.WriteCloser, string, error) {
	p := filepath.Join(s.Dir, "logs", id+".log")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, "", err
	}
	f, err := os.Create(p)
	return f, p, err
}

func writeJSON(p string, v any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
