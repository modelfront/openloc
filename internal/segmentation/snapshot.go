package segmentation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
)

// Snapshot is the rule set a file was imported with, stored on the file row in
// full: later edits, deletes and new built-in versions never change how an
// existing file parses. A file without one uses ConservativeID.
type Snapshot struct {
	RuleSet RuleSet        `json:"rule_set"`
	Source  SnapshotSource `json:"source"`
	// Language is the normalized source language of the project at upload,
	// empty for a project without one.
	Language string `json:"language"`
	// ProfileID is the segmentation profile the rules came from.
	ProfileID int64 `json:"profile_id,omitempty"`
}

// SnapshotSource names the set a snapshot was taken from, for display.
type SnapshotSource struct {
	// Kind is "builtin" or "org".
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

const (
	SourceKindBuiltin = "builtin"
	SourceKindOrg     = "org"
)

// ForSnapshot returns the segmenter for a file's stored snapshot; empty or
// JSON null is a file stored without one, which uses Conservative.
func ForSnapshot(raw []byte) (*Segmenter, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Conservative(), nil
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("read segmentation snapshot: %w", err)
	}
	return Cached(s.RuleSet)
}

// SnapshotOrigin returns the language and profile a stored snapshot was taken
// for; ok is false for a file stored without a snapshot.
func SnapshotOrigin(raw []byte) (lang string, profileID int64, ok bool, err error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", 0, false, nil
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", 0, false, fmt.Errorf("read segmentation snapshot: %w", err)
	}
	return s.Language, s.ProfileID, true, nil
}

// compiledCache holds compiled segmenters by the hash of their rule set, so an
// export does not recompile the same rules for every file.
var compiledCache = struct {
	sync.Mutex
	m map[[sha256.Size]byte]*Segmenter
}{m: map[[sha256.Size]byte]*Segmenter{}}

// maxCached bounds compiledCache; it is emptied when full.
const maxCached = 256

// Cached is Compile with a cache keyed by the rule set's content.
func Cached(rs RuleSet) (*Segmenter, error) {
	rs = Normalize(rs)
	raw, err := json.Marshal(rs)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(raw)
	compiledCache.Lock()
	s, ok := compiledCache.m[key]
	compiledCache.Unlock()
	if ok {
		return s, nil
	}
	s, err = Compile(rs)
	if err != nil {
		return nil, err
	}
	compiledCache.Lock()
	if len(compiledCache.m) >= maxCached {
		clear(compiledCache.m)
	}
	compiledCache.m[key] = s
	compiledCache.Unlock()
	return s, nil
}
