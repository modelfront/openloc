package llmdebug_test

// Real-S3 check for the persisted LLM debug log. The
// unit tests in store_test.go use a fake objectStore, which proves the batching
// and key logic but not the half that actually talks S3: the aws-sdk client,
// pagination in ListKeys, and DeleteObjects' habit of reporting per-key failures
// inside a 200 response.
//
// OPT-IN: skipped unless LLMDEBUG_S3_ENDPOINT is set, because the Go suite
// otherwise uses ephemeral testcontainers and must not depend on the dev docker
// stack being up. Run it against the dev storage (SeaweedFS) with:
//
//	LLMDEBUG_S3_ENDPOINT=http://localhost:9004 \
//	  go test ./internal/llmdebug/ -run Integration_S3 -v
//
// (Port from instance.conf's S3_PORT; creds are the compose defaults.)

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"openloc/internal/file"
	"openloc/internal/llmdebug"
)

func s3Storage(t *testing.T) *file.Storage {
	t.Helper()
	endpoint := os.Getenv("LLMDEBUG_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set LLMDEBUG_S3_ENDPOINT (e.g. http://localhost:9004) to run the real-S3 check")
	}
	key := envOrDefault("LLMDEBUG_S3_ACCESS_KEY", "changeme-s3-access-key")
	secret := envOrDefault("LLMDEBUG_S3_SECRET_KEY", "changeme-s3-secret-key")
	bucket := envOrDefault("LLMDEBUG_S3_BUCKET", "openloc")

	st := file.NewStorage(endpoint, key, secret, bucket)
	if err := st.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("S3 storage at %s is not reachable: %v", endpoint, err)
	}
	return st
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestIntegration_S3StoreWritesAndPrunes walks the whole lifecycle against a
// live S3 store: write batches under two different date prefixes, read one back,
// then prune and confirm only the aged prefix went.
func TestIntegration_S3StoreWritesAndPrunes(t *testing.T) {
	st := s3Storage(t)
	ctx := context.Background()

	// A test-scoped prefix so a failed run can never delete real debug objects
	// (and so parallel runs don't fight).
	prefix := fmt.Sprintf("llm-debug-itest-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		keys, err := st.ListKeys(context.Background(), prefix+"/")
		if err != nil {
			t.Logf("cleanup list failed: %v", err)
			return
		}
		if len(keys) > 0 {
			if err := st.DeleteKeys(context.Background(), keys); err != nil {
				t.Logf("cleanup delete failed: %v", err)
			}
		}
	})

	today := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	old := today.AddDate(0, 0, -5)

	// Two writers on the same bucket, distinct pods — the prod shape.
	writeBatch := func(host string, when time.Time, entries ...llmdebug.Entry) {
		s := llmdebug.NewStore(st, llmdebug.StoreOpts{
			KeyPrefix: prefix,
			BatchSize: len(entries),
			// Long interval: the batch must flush on count, so the test never
			// races a timer.
			FlushInterval: time.Hour,
			Host:          host,
			Now:           func() time.Time { return when },
		})
		for _, e := range entries {
			s.Record(ctx, e)
		}
		closeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := s.Close(closeCtx); err != nil {
			t.Fatalf("close store: %v", err)
		}
		if got := s.Stats(); got.Written != 1 || got.Failed != 0 || got.Dropped != 0 {
			t.Fatalf("stats after one batch = %+v, want Written=1 Failed=0 Dropped=0", got)
		}
	}

	// Today: two pods, one batch each.
	writeBatch("pod-a", today,
		llmdebug.Entry{Kind: llmdebug.KindMT, Provider: "claude", Status: 200,
			// Quotes and newlines are the whole point — these must survive S3
			// and come back byte-identical.
			RequestBody: "{\"segments\":[{\"id\":\"1\",\"content\":\"He said \\\"stop\\\".\"}]}",
			User:        "line one\nline two",
		},
		llmdebug.Entry{Kind: llmdebug.KindExtension, Provider: "ext:mockqe", Status: 500, Error: "boom"},
	)
	writeBatch("pod-b", today,
		llmdebug.Entry{Kind: llmdebug.KindAPE, Provider: "claude", Status: 200},
	)
	// Five days ago: the prune target.
	writeBatch("pod-a", old, llmdebug.Entry{Kind: llmdebug.KindMT, Provider: "openai", Status: 200})

	keys, err := st.ListKeys(ctx, prefix+"/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("want 3 objects (2 pods today + 1 old), got %d: %v", len(keys), keys)
	}

	// Both pods wrote under today's prefix without overwriting each other.
	todayPrefix := prefix + "/" + today.Format("2006-01-02") + "/"
	var todayKeys []string
	for _, k := range keys {
		if strings.HasPrefix(k, todayPrefix) {
			todayKeys = append(todayKeys, k)
		}
	}
	if len(todayKeys) != 2 {
		t.Errorf("want 2 objects under %s, got %d: %v", todayPrefix, len(todayKeys), todayKeys)
	}

	// Read the two-entry batch back and check it round-tripped exactly.
	var found bool
	for _, k := range todayKeys {
		rc, err := st.Download(ctx, k)
		if err != nil {
			t.Fatalf("download %s: %v", k, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", k, err)
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) != 2 {
			continue
		}
		found = true
		var first llmdebug.Entry
		if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
			t.Fatalf("line 1 of %s is not JSON: %v", k, err)
		}
		if want := "{\"segments\":[{\"id\":\"1\",\"content\":\"He said \\\"stop\\\".\"}]}"; first.RequestBody != want {
			t.Errorf("request body round-trip:\n got %q\nwant %q", first.RequestBody, want)
		}
		if first.User != "line one\nline two" {
			t.Errorf("embedded newline did not survive: %q", first.User)
		}
		if first.Time.IsZero() {
			t.Error("Time was not stamped")
		}
	}
	if !found {
		t.Error("no object held the 2-entry batch — batching or NDJSON framing is wrong")
	}

	// Prune with a clock 5 days after `old`: only the aged prefix goes.
	pruner := llmdebug.NewPruner(st, llmdebug.PrunerOpts{
		KeyPrefix:  prefix,
		RetainDays: llmdebug.DefaultRetainDays,
		Now:        func() time.Time { return today },
	})
	deleted, err := pruner.Prune(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("pruned %d objects, want 1 (only the 5-day-old prefix)", deleted)
	}

	after, err := st.ListKeys(ctx, prefix+"/")
	if err != nil {
		t.Fatalf("list after prune: %v", err)
	}
	if len(after) != 2 {
		t.Errorf("after prune: %d objects remain, want 2: %v", len(after), after)
	}
	for _, k := range after {
		if !strings.HasPrefix(k, todayPrefix) {
			t.Errorf("survivor %q is not under today's prefix", k)
		}
	}

	// A second prune is a no-op rather than an error.
	if n, err := pruner.Prune(ctx); err != nil || n != 0 {
		t.Errorf("second prune = (%d, %v), want (0, nil)", n, err)
	}
}

// TestIntegration_S3ReaderServesWhatTheStoreWrote closes the loop against a
// live S3 store: the Reader must read back exactly what the Store wrote, in time
// order across both pods, with a cursor that advances. The unit tests fake the
// storage client, so this is the only place the real list/get path is exercised.
func TestIntegration_S3ReaderServesWhatTheStoreWrote(t *testing.T) {
	st := s3Storage(t)
	ctx := context.Background()

	prefix := fmt.Sprintf("llm-debug-itest-reader-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		keys, err := st.ListKeys(context.Background(), prefix+"/")
		if err == nil && len(keys) > 0 {
			if err := st.DeleteKeys(context.Background(), keys); err != nil {
				t.Logf("cleanup delete failed: %v", err)
			}
		}
	})

	when := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	write := func(host string, entries ...llmdebug.Entry) {
		s := llmdebug.NewStore(st, llmdebug.StoreOpts{
			KeyPrefix: prefix, BatchSize: len(entries), FlushInterval: time.Hour,
			Host: host, Now: func() time.Time { return when },
		})
		for _, e := range entries {
			s.Record(ctx, e)
		}
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := s.Close(c); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	// Two pods, interleaved in time — what the viewer has to linearise.
	write("pod-a",
		llmdebug.Entry{Kind: llmdebug.KindMT, Provider: "claude", Time: when.Add(1 * time.Second)},
		llmdebug.Entry{Kind: llmdebug.KindMT, Provider: "claude", Time: when.Add(3 * time.Second)},
	)
	write("pod-b",
		llmdebug.Entry{Kind: llmdebug.KindExtension, Provider: "ext:mockqe", Time: when.Add(2 * time.Second),
			RequestBody: "{\"rows\":[{\"original\":\"He said \\\"stop\\\".\"}]}"},
	)

	reader := llmdebug.NewReader(st, prefix)
	got, cursor := reader.Since(0)
	if len(got) != 3 {
		t.Fatalf("reader returned %d entries, want 3", len(got))
	}
	wantKinds := []string{llmdebug.KindMT, llmdebug.KindExtension, llmdebug.KindMT}
	for i, want := range wantKinds {
		if got[i].Kind != want {
			t.Errorf("entry %d kind = %s, want %s — both pods' objects must merge by time",
				i, got[i].Kind, want)
		}
	}
	if got[1].RequestBody == "" || !strings.Contains(got[1].RequestBody, `He said \"stop\".`) {
		t.Errorf("escaped body did not survive the S3 round trip: %q", got[1].RequestBody)
	}
	if cursor == 0 {
		t.Error("cursor did not advance")
	}

	// Polling with that cursor returns nothing new...
	if fresh, c2 := reader.Since(cursor); len(fresh) != 0 || c2 != cursor {
		t.Errorf("re-poll returned %d entries (cursor %d → %d), want none", len(fresh), cursor, c2)
	}

	// ...and a brand-new Reader (i.e. the other replica) agrees on the cursor,
	// which is the property that makes a pod-local sequence unusable.
	if fresh, _ := llmdebug.NewReader(st, prefix).Since(cursor); len(fresh) != 0 {
		t.Errorf("a second replica re-served %d entries for the same cursor", len(fresh))
	}

	// A later call shows up for both.
	write("pod-a", llmdebug.Entry{Kind: llmdebug.KindAPE, Time: when.Add(30 * time.Second)})
	fresh, _ := reader.Since(cursor)
	if len(fresh) != 1 || fresh[0].Kind != llmdebug.KindAPE {
		t.Errorf("after a new batch got %+v, want just the APE entry", fresh)
	}
}
