package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RetentionOptions controls a read-only retention plan.
type RetentionOptions struct {
	// Keep is the number of newest archives to keep. Default 7.
	Keep int
	// MinAge protects archives newer than this age from deletion. Zero disables.
	MinAge time.Duration
	// Now is for tests; zero means time.Now.
	Now time.Time
}

// RetentionEntry is one planned action. PlanRetention never deletes anything;
// callers must show/confirm the plan and perform cleanup themselves.
type RetentionEntry struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Action    string    `json:"action"` // keep | delete-candidate
	Reason    string    `json:"reason"`
}

// PlanRetention returns a read-only plan for backup archives below
// <dataDir>/backups. It never removes files.
func PlanRetention(dataDir string, opts RetentionOptions) ([]RetentionEntry, error) {
	dir, err := resolveDir(dataDir)
	if err != nil {
		return nil, err
	}
	if opts.Keep <= 0 {
		opts.Keep = 7
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	backupsDir := filepath.Join(dir, DefaultBackupDir)
	list, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []RetentionEntry{}, nil
		}
		return nil, fmt.Errorf("read backups: %w", err)
	}
	type candidate struct {
		path string
		size int64
		mod  time.Time
	}
	items := make([]candidate, 0, len(list))
	for _, e := range list {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
			continue
		}
		p := filepath.Join(backupsDir, e.Name())
		st, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		items = append(items, candidate{path: p, size: st.Size(), mod: st.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	out := make([]RetentionEntry, 0, len(items))
	for i, it := range items {
		entry := RetentionEntry{Path: it.path, Size: it.size, CreatedAt: it.mod, Action: "keep"}
		switch {
		case i < opts.Keep:
			entry.Reason = "newest archives up to keep count"
		case opts.MinAge > 0 && opts.Now.Sub(it.mod) < opts.MinAge:
			entry.Reason = "younger than min age"
		default:
			entry.Action = "delete-candidate"
			entry.Reason = "beyond keep count and older than min age"
		}
		out = append(out, entry)
	}
	return out, nil
}
