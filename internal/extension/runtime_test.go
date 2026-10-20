package extension

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"openloc/internal/extension/exttest"
	"openloc/internal/llmdebug"
	"openloc/pkg/extsdk"
)

type captureRec struct {
	mu      sync.Mutex
	entries []llmdebug.Entry
}

func (c *captureRec) Record(_ context.Context, e llmdebug.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func mockPackage(t *testing.T) Package {
	t.Helper()
	p, err := ReadPackage(exttest.Zip(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func predictCall(p Package, token, baseURL string, rows ...extsdk.PredictRow) Call {
	return Call{
		Manifest: p.Manifest, Wasm: p.load, SHA256: p.SHA256, Function: extsdk.FuncPredict,
		Token: token, BaseURL: baseURL,
		Input: extsdk.PredictRequest{
			Context: extsdk.Context{SourceLang: "en", TargetLang: "de", Settings: map[string]string{extsdk.SettingBaseURL: baseURL}},
			Rows:    rows,
		},
	}
}

func TestInvoke_LocalVerdicts(t *testing.T) {
	p := mockPackage(t)
	rt := NewRuntime(nil, nil)
	var out extsdk.PredictResponse
	err := rt.Invoke(context.Background(), predictCall(p, "tok", "",
		extsdk.PredictRow{ID: "1", Source: "a", Target: "good"},
		extsdk.PredictRow{ID: "2", Source: "b", Target: "BAD"},
		extsdk.PredictRow{ID: "3", Source: "c", Target: "FIXME here"},
	), &out)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]extsdk.PredictResult{}
	for _, r := range out.Rows {
		got[r.ID] = r
	}
	if !got["1"].Approved || got["2"].Approved {
		t.Fatalf("verdicts = %+v", out.Rows)
	}
	if pe := got["3"].Postedit; pe == nil || *pe != "fixed here" || got["3"].PosteditApproved == nil || !*got["3"].PosteditApproved {
		t.Fatalf("postedit = %+v", got["3"])
	}
}

// The token reaches the vendor, but never the extension's input or the debug
// record.
func TestInvoke_SecretInjectedOnlyOnTheWire(t *testing.T) {
	const token = "s3cr3t-token"
	var gotToken string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("token")
		body, _ := io.ReadAll(r.Body)
		var rows []extsdk.PredictRow
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Errorf("vendor body: %v", err)
		}
		if strings.Contains(string(body), token) {
			t.Errorf("token leaked into body sent by extension")
		}
		_ = json.NewEncoder(w).Encode(extsdk.PredictResponse{Rows: []extsdk.PredictResult{{ID: rows[0].ID, Approved: true}}})
	}))
	defer srv.Close()

	p := mockPackage(t)
	rec := &captureRec{}
	rt := NewRuntime(srv.Client(), rec)
	var out extsdk.PredictResponse
	if err := rt.Invoke(context.Background(), predictCall(p, token, srv.URL, extsdk.PredictRow{ID: "7", Source: "a", Target: "b"}), &out); err != nil {
		t.Fatal(err)
	}
	if gotToken != token {
		t.Fatalf("vendor got token %q, want %q", gotToken, token)
	}
	if len(out.Rows) != 1 || out.Rows[0].ID != "7" || !out.Rows[0].Approved {
		t.Fatalf("out = %+v", out)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("recorded %d entries, want 1", len(rec.entries))
	}
	e := rec.entries[0]
	if strings.Contains(e.URL+e.RequestBody+e.ResponseBody, token) {
		t.Fatalf("token in debug record: %+v", e)
	}
	if !strings.Contains(e.URL, extsdk.SecretPlaceholder) || e.Provider != "ext:mockqe" || e.Kind != llmdebug.KindExtension {
		t.Fatalf("record = %+v", e)
	}
}

func TestInvoke_BlocksHostsNotAllowed(t *testing.T) {
	called := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	p := mockPackage(t)
	p.Manifest.AllowedHosts = []string{"api.example.com"}
	rt := NewRuntime(srv.Client(), nil)
	err := rt.Invoke(context.Background(), predictCall(p, "tok", srv.URL, extsdk.PredictRow{ID: "1", Target: "x"}), nil)
	var ce *CallError
	if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "not in allowed_hosts") {
		t.Fatalf("err = %v, want allowed_hosts refusal", err)
	}
	if called {
		t.Fatal("blocked host was called")
	}
}

// A transport error quotes the request URL, which carries the token. The host
// strips the token, plain and URL-escaped, before the error reaches the guest.
func TestInvoke_ErrorFromExtensionIsRedacted(t *testing.T) {
	const token = "s3cr3t/+=tok"
	escaped := url.QueryEscape(token)
	if escaped == token {
		t.Fatal("token must escape differently, or the escaped form is not tested")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "https://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	p := mockPackage(t)
	err = NewRuntime(&http.Client{}, nil).Invoke(context.Background(), Call{
		Manifest: p.Manifest, Wasm: p.load, SHA256: p.SHA256, Function: extsdk.FuncCheckKey,
		Token: token, BaseURL: base,
		Input: extsdk.CheckKeyRequest{Context: extsdk.Context{Settings: map[string]string{extsdk.SettingBaseURL: base}}},
	}, nil)
	var ce *CallError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a CallError from the failed dial", err)
	}
	for _, text := range []string{ce.Msg, err.Error()} {
		if strings.Contains(text, token) || strings.Contains(text, escaped) {
			t.Fatalf("error leaks the token: %q", text)
		}
	}
	if !strings.Contains(ce.Msg, "/check?token=REDACTED") {
		t.Fatalf("msg = %q, want the URL with the token replaced by REDACTED", ce.Msg)
	}
}

func TestInvoke_UnknownFunction(t *testing.T) {
	p := mockPackage(t)
	err := NewRuntime(nil, nil).Invoke(context.Background(), Call{
		Manifest: p.Manifest, Wasm: p.load, SHA256: p.SHA256, Function: "nope", Input: struct{}{},
	}, nil)
	if !errors.Is(err, ErrNoFunction) {
		t.Fatalf("err = %v, want ErrNoFunction", err)
	}
}

// An allowed host cannot send the call on to a host the manifest does not
// list by answering with a redirect.
func TestInvoke_RedirectToHostNotAllowedIsRefused(t *testing.T) {
	const token = "tok-redirect"
	var otherCalled bool
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCalled = true
		_ = json.NewEncoder(w).Encode(extsdk.PredictResponse{})
	}))
	defer other.Close()
	allowed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer allowed.Close()

	p := mockPackage(t)
	err := NewRuntime(allowed.Client(), nil).Invoke(context.Background(),
		predictCall(p, token, allowed.URL, extsdk.PredictRow{ID: "1", Target: "x"}), nil)
	if otherCalled {
		t.Fatal("the redirect reached a host that is not in allowed_hosts")
	}
	var ce *CallError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want the extension to report the refused redirect", err)
	}
	if strings.Contains(ce.Msg, token) {
		t.Fatalf("error leaks the token: %q", ce.Msg)
	}
}

// The default client refuses internal addresses: an admin chooses the
// endpoint, so a loopback URL must not reach the server's own network.
func TestInvoke_DefaultClientRefusesInternalAddress(t *testing.T) {
	called := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	p := mockPackage(t)
	err := NewRuntime(nil, nil).Invoke(context.Background(), predictCall(p, "tok", srv.URL, extsdk.PredictRow{ID: "1", Target: "x"}), nil)
	var ce *CallError
	if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "non-public") {
		t.Fatalf("err = %v, want the non-public refusal", err)
	}
	if called {
		t.Fatal("internal address was called")
	}
}

// When the extension fails after the vendor answered, the error carries that
// status, so the caller can tell a bad request from a vendor outage.
func TestInvoke_ErrorCarriesVendorStatus(t *testing.T) {
	for _, status := range []int{400, 429, 503} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		p := mockPackage(t)
		err := NewRuntime(srv.Client(), nil).Invoke(context.Background(), predictCall(p, "tok", srv.URL, extsdk.PredictRow{ID: "1", Target: "x"}), nil)
		srv.Close()
		var ce *CallError
		if !errors.As(err, &ce) {
			t.Fatalf("status %d: err = %v, want CallError", status, err)
		}
		if ce.VendorStatus != status {
			t.Errorf("VendorStatus = %d, want %d", ce.VendorStatus, status)
		}
	}
}
