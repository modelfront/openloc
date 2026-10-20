package publicapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"openloc/internal/testutil"
)

// v1Client sends requests to /api/v1 with an access key and checks every
// request and response against the OpenAPI spec: an undocumented status, a
// missing required field or a wrongly typed value fails the test. That is
// what keeps the DTOs and the spec from drifting apart.
type v1Client struct {
	t      *testing.T
	router http.Handler
	spec   routers.Router
}

func newV1Client(t *testing.T, router http.Handler) *v1Client {
	t.Helper()
	// httptest requests are addressed to example.com; the served spec uses a
	// relative server URL, which the matcher cannot resolve on its own.
	doc, err := openapi3.NewLoader().LoadFromData(testutil.APISpec().JSON)
	if err != nil {
		t.Fatal(err)
	}
	doc.Servers = openapi3.Servers{{URL: "http://example.com/api/v1"}}
	specRouter, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return &v1Client{t: t, router: router, spec: specRouter}
}

// succeeded holds the operationId of every call that got a 2xx through a
// v1Client, so TestMain can tell which operations no test ever exercised
// successfully against the spec.
var succeeded sync.Map

// integrationRan is set once a test has a database, so the coverage check
// stays quiet when every integration test skipped.
var integrationRan atomic.Bool

// TestMain fails a full package run in which some spec operation never got a
// 2xx through a v1Client: its success response was never checked against the
// spec. The check only runs when the whole integration set ran — not under
// -short, -run or -skip, and not when the tests failed already.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if missing := operationsWithoutSuccess(); len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "FAIL: no test got a 2xx through the validating client for: %s\n", strings.Join(missing, ", "))
			code = 1
		}
	}
	os.Exit(code)
}

func operationsWithoutSuccess() []string {
	if testing.Short() || !integrationRan.Load() {
		return nil
	}
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return nil
		}
	}
	var missing []string
	for _, item := range testutil.APISpec().Doc.Paths.Map() {
		for _, op := range item.Operations() {
			if _, ok := succeeded.Load(op.OperationID); !ok {
				missing = append(missing, op.OperationID)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// do sends a JSON request (nil body = none) with key as the bearer token.
func (c *v1Client) do(method, path string, body any, key string) *httptest.ResponseRecorder {
	c.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			c.t.Fatal(err)
		}
	}
	return c.send(method, path, raw, "application/json", key)
}

// upload sends a multipart file upload in the `file` field.
func (c *v1Client) upload(path, filename, content, key string) *httptest.ResponseRecorder {
	c.t.Helper()
	return c.uploadWith(path, filename, []byte(content), nil, key)
}

// uploadWith uploads content with extra form fields next to the file.
func (c *v1Client) uploadWith(path, filename string, content []byte, fields map[string]string, key string) *httptest.ResponseRecorder {
	c.t.Helper()
	body, contentType := testutil.MultipartBody(c.t, filename, content, fields)
	return c.send(http.MethodPost, path, body.Bytes(), contentType, key)
}

func (c *v1Client) send(method, path string, body []byte, contentType, key string) *httptest.ResponseRecorder {
	c.t.Helper()
	newReq := func() *http.Request {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		if body != nil {
			req.Header.Set("Content-Type", contentType)
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		return req
	}

	rec := httptest.NewRecorder()
	c.router.ServeHTTP(rec, newReq())

	// The router consumed the body; validation reads a fresh copy, with the
	// absolute URL the spec matcher compares against its server.
	req := newReq()
	req.URL.Scheme, req.URL.Host = "http", "example.com"
	route, params, err := c.spec.FindRoute(req)
	if err != nil {
		c.t.Fatalf("%s %s: not in the spec: %v", method, path, err)
	}
	if rec.Code >= 200 && rec.Code < 300 {
		succeeded.Store(route.Operation.OperationID, true)
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	// A request the test sends wrong on purpose (an empty body to probe a
	// gate) is allowed to fail request validation; only 2xx requests must
	// match the spec.
	reqErr := openapi3filter.ValidateRequest(context.Background(), in)
	if reqErr != nil && rec.Code < 300 {
		c.t.Errorf("%s %s: request does not match the spec but got %d: %v", method, path, rec.Code, reqErr)
	}

	opts := &openapi3filter.Options{IncludeResponseStatus: true}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		opts.ExcludeResponseBody = true
	}
	in.Options = opts
	if err := openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		Options:                opts,
	}); err != nil {
		c.t.Errorf("%s %s: %d response does not match the spec: %v\n  body: %s", method, path, rec.Code, err, rec.Body.String())
	}
	if !opts.ExcludeResponseBody {
		for _, field := range undocumentedFields(route.Operation, rec.Code, rec.Body.Bytes()) {
			c.t.Errorf("%s %s: %d response field %s is not in the spec", method, path, rec.Code, field)
		}
	}
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return env.Error.Code
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

// httpRequest builds a request without spec validation, for probes the spec
// does not describe (conditional GETs, anonymous calls, app routes).
func httpRequest(method, path, key string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req
}

func serve(router http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
