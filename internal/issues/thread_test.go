package issues

import (
	"testing"
	"time"
)

// An issue's thread id names the issue for good: the same issue always gets
// the same id, and an issue that reuses a number after a database reset, or
// the same number on another deployment, gets another one.
func TestIssueThreadIsUniquePerIssue(t *testing.T) {
	filed := time.Date(2026, 10, 19, 12, 0, 0, 123456000, time.UTC)
	a := issueThread("https://app.example.com", 1, filed)
	if again := issueThread("https://app.example.com", 1, filed); again.ID != a.ID {
		t.Errorf("the same issue got two thread ids: %q and %q", a.ID, again.ID)
	}
	if reused := issueThread("https://app.example.com", 1, filed.Add(time.Hour)); reused.ID == a.ID {
		t.Errorf("issue #1 after a reset got the old thread id %q", a.ID)
	}
	if other := issueThread("https://staging.example.com", 1, filed); other.ID == a.ID {
		t.Errorf("two deployments share thread id %q", a.ID)
	}
}
