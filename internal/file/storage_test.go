package file

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A per-key DeleteObjects failure returns an error without the object key:
// keys can carry a customer's file name, and the error reaches error tracking.
func TestDeleteKeys_PerKeyFailureOmitsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if _, err := w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Error><Key>chat-uploads/1/2/ab-secret-contract.pdf</Key><Code>AccessDenied</Code><Message>Access Denied</Message></Error>
</DeleteResult>`)); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()

	st := NewStorage(srv.URL, "key", "secret", "bucket")
	err := st.DeleteKeys(context.Background(), []string{"chat-uploads/1/2/ab-secret-contract.pdf", "other"})
	if err == nil {
		t.Fatal("DeleteKeys: want an error for the per-key failure")
	}
	if strings.Contains(err.Error(), "secret-contract") {
		t.Errorf("error names the object key: %v", err)
	}
	if !strings.Contains(err.Error(), "1 of 2 failed") || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error = %v, want the failure count and the S3 error code", err)
	}
}
