package llmdebug

import (
	"context"
	"sort"
	"time"
)

// Snapshotter returns every entry the source holds, newest first, and whether
// that is all of them: false when the caller's deadline cut the read short. *Ring
// and *Reader implement it. Searches (the MCP log tools) filter a snapshot
// rather than poll Since, which keeps only the newest maxPerPoll.
type Snapshotter interface {
	Snapshot(ctx context.Context) (entries []Entry, complete bool)
}

// Log is a source the viewer polls and the log tools search: *Ring or *Reader.
type Log interface {
	Source
	Snapshotter
}

// Filter narrows a snapshot. Zero fields match everything.
type Filter struct {
	JobID  int64
	FileID int64
	// FileIDs, when non-nil, keeps only entries whose FileID is in the set. An
	// entry without a file never matches a non-nil set.
	FileIDs map[int64]bool
	Kind    string
	From    time.Time // inclusive
	To      time.Time // exclusive
	// Before is a page cursor: only entries with Seq < Before.
	Before uint64
}

// Match reports whether e passes every set field of f.
func (f Filter) Match(e Entry) bool {
	switch {
	case f.JobID != 0 && e.JobID != f.JobID:
		return false
	case f.FileID != 0 && e.FileID != f.FileID:
		return false
	case f.FileIDs != nil && !f.FileIDs[e.FileID]:
		return false
	case f.Kind != "" && e.Kind != f.Kind:
		return false
	case !f.From.IsZero() && e.Time.Before(f.From):
		return false
	case !f.To.IsZero() && !e.Time.Before(f.To):
		return false
	case f.Before != 0 && e.Seq >= f.Before:
		return false
	}
	return true
}

// Page returns up to limit matching entries of a newest-first snapshot, and
// the cursor for the next page (0 when there is none).
func Page(entries []Entry, f Filter, limit int) (page []Entry, next uint64) {
	for _, e := range entries {
		if !f.Match(e) {
			continue
		}
		if len(page) == limit {
			return page, page[len(page)-1].Seq
		}
		page = append(page, e)
	}
	return page, 0
}

// GroupBy values.
const (
	GroupByJob  = "job"
	GroupByFile = "file"
)

// Group is one job's or one file's calls in a snapshot.
type Group struct {
	JobID   int64     `json:"job_id"`
	FileID  int64     `json:"file_id"`
	Project string    `json:"project"`
	Calls   int       `json:"calls"`
	Errors  int       `json:"errors"`  // a transport failure or a non-2xx status
	Retries int       `json:"retries"` // split-and-retry requests
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
}

// Groups counts the matching entries per job (by == GroupByJob) or per file,
// most recent group first. Entries without a job (or file) form the group
// with a zero id. The second result is how many groups there were in all,
// before the cut to limit.
func Groups(entries []Entry, f Filter, by string, limit int) ([]Group, int) {
	idx := map[[2]int64]int{}
	var out []Group
	for _, e := range entries {
		if !f.Match(e) {
			continue
		}
		key := [2]int64{0, e.FileID}
		if by == GroupByJob {
			key = [2]int64{e.JobID, 0}
		}
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, Group{JobID: key[0], FileID: key[1], First: e.Time, Last: e.Time})
		}
		g := &out[i]
		// A job lives in one file and a file in one project; the group of calls
		// without a job (or file) spans many, so it names neither.
		if key != ([2]int64{}) {
			if by == GroupByJob && g.FileID == 0 {
				g.FileID = e.FileID
			}
			if g.Project == "" {
				g.Project = e.Project
			}
		}
		g.Calls++
		if e.Status < 200 || e.Status > 299 {
			g.Errors++
		}
		if e.Retry {
			g.Retries++
		}
		if e.Time.Before(g.First) {
			g.First = e.Time
		}
		if e.Time.After(g.Last) {
			g.Last = e.Time
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	total := len(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, total
}

// FileIDs returns the distinct non-zero file ids of a snapshot.
func FileIDs(entries []Entry) []int64 {
	seen := map[int64]bool{}
	var ids []int64
	for _, e := range entries {
		if e.FileID != 0 && !seen[e.FileID] {
			seen[e.FileID] = true
			ids = append(ids, e.FileID)
		}
	}
	return ids
}
