package translationmemory

import "time"

// SetSearchTimeouts lets the external tests force the entry-search time limits.
func SetSearchTimeouts(s *Service, count, page time.Duration) {
	s.countTimeout = count
	s.searchTimeout = page
}

// SetImportBatchHook runs f after each batch an import writes, and returns a
// func that removes it.
func SetImportBatchHook(f func(importID int64)) func() {
	importBatchHook = f
	return func() { importBatchHook = nil }
}

// ImportSpoolDir is where TMX uploads wait for their import.
func ImportSpoolDir() string { return importSpoolDir() }
