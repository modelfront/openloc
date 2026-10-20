package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/extension"
	"openloc/internal/httputil"
)

// A setup problem that no retry can fix fails the job at once, with a
// message the user can act on.
func TestRunAIStep_ExtensionSetupErrorsArePermanent(t *testing.T) {
	for _, sentinel := range []error{extension.ErrCannotReview, extension.ErrNotConfigured} {
		mq := extStepJob("good")
		svc := NewService(mq, &passthroughTxRunner{q: mq}, nil).SetAIProvider(nil, nil, identityDecryptor{})
		svc.SetExtensions(&fakeExtensions{err: fmt.Errorf("key 7: %w", sentinel)})
		_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
		if !IsPermanent(err) {
			t.Errorf("%v: want a permanent error, got %v", sentinel, err)
		}
		if _, ok := httputil.AsClientError(err); !ok {
			t.Errorf("%v: want a client error, got %v", sentinel, err)
		}
	}
}

// A stored module that fails its digest check is damaged; retrying cannot
// fix it, and "did not respond" would send the user the wrong way.
func TestRunAIStep_DamagedModuleIsPermanent(t *testing.T) {
	mq := extStepJob("good")
	pred := &fakeExtPredictor{err: fmt.Errorf("extension mockqe: %w", extension.ErrModuleDamaged)}
	svc, _ := newExtStepService(mq, pred)
	_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	msg, ok := httputil.AsClientError(err)
	if !ok || !IsPermanent(err) {
		t.Fatalf("want a permanent client error, got %v", err)
	}
	if msg == "" || strings.Contains(msg, "did not respond") {
		t.Errorf("message = %q, want one that says the file is damaged", msg)
	}
}
