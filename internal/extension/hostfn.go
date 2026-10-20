package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	extism "github.com/extism/go-sdk"
	"golang.org/x/time/rate"

	"openloc/internal/llmdebug"
	"openloc/pkg/extsdk"
)

const (
	maxRequestBody  = 8 << 20
	maxResponseBody = 8 << 20
	// maxRequestsPerCall stops a looping extension.
	maxRequestsPerCall = 1000
	maxRedirects       = 5
)

// callEnv is the per-call state the http_request host function reads from
// the call context.
type callEnv struct {
	extID    string
	token    string
	hosts    map[string]bool
	client   *http.Client
	rec      llmdebug.Recorder
	limiter  *rate.Limiter
	requests int
	// lastStatus is the status of the last vendor response, 0 before one.
	lastStatus int
}

type callEnvKey struct{}

func withCallEnv(ctx context.Context, env *callEnv) context.Context {
	return context.WithValue(ctx, callEnvKey{}, env)
}

// newCallEnv builds the call state. limiter paces the vendor requests (nil:
// no limit); the Runtime shares it between calls of one extension and key.
func newCallEnv(c Call, client *http.Client, rec llmdebug.Recorder, limiter *rate.Limiter) (*callEnv, error) {
	env := &callEnv{extID: c.Manifest.ID, token: c.Token, hosts: map[string]bool{}, client: client, rec: rec, limiter: limiter}
	for _, h := range c.Manifest.AllowedHosts {
		if h != BaseURLHost {
			env.hosts[h] = true
			continue
		}
		if c.BaseURL == "" {
			continue
		}
		u, err := httpsURL(c.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("endpoint: %w", err)
		}
		env.hosts[strings.ToLower(u.Host)] = true
	}
	return env, nil
}

// allowed reports whether u may be called: https, and its host (with port
// when the entry has one) is listed.
func (env *callEnv) allowed(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return env.hosts[host] || env.hosts[strings.ToLower(u.Hostname())] && u.Port() == ""
}

// redactBytes is redact for a response body.
func (env *callEnv) redactBytes(b []byte) []byte {
	if env.token == "" || len(b) == 0 {
		return b
	}
	b = bytes.ReplaceAll(b, []byte(env.token), []byte("REDACTED"))
	return bytes.ReplaceAll(b, []byte(url.QueryEscape(env.token)), []byte("REDACTED"))
}

// redact removes the token from text that leaves the host function.
func (env *callEnv) redact(s string) string {
	if env.token == "" {
		return s
	}
	s = strings.ReplaceAll(s, env.token, "REDACTED")
	return strings.ReplaceAll(s, url.QueryEscape(env.token), "REDACTED")
}

func httpHostFunction() extism.HostFunction {
	return extism.NewHostFunctionWithStack(extsdk.HostFunction,
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			resp := handleHTTP(ctx, p, stack[0])
			out, err := json.Marshal(resp)
			if err != nil {
				out = []byte(`{"error":"encode response"}`)
			}
			offset, err := p.WriteBytes(out)
			if err != nil {
				panic(err) // guest memory is exhausted; aborts the call
			}
			stack[0] = offset
		},
		[]extism.ValueType{extism.ValueTypePTR}, []extism.ValueType{extism.ValueTypePTR})
}

func handleHTTP(ctx context.Context, p *extism.CurrentPlugin, offset uint64) extsdk.HTTPResponse {
	env, ok := ctx.Value(callEnvKey{}).(*callEnv)
	if !ok {
		return extsdk.HTTPResponse{Error: "http is not available"}
	}
	raw, err := p.ReadBytes(offset)
	if err != nil {
		return extsdk.HTTPResponse{Error: "read request"}
	}
	var req extsdk.HTTPRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return extsdk.HTTPResponse{Error: "invalid request: " + err.Error()}
	}
	return env.do(ctx, req)
}

// do sends req and returns the response with the token removed from every
// field, so a vendor that echoes the key cannot hand it to the module.
func (env *callEnv) do(ctx context.Context, req extsdk.HTTPRequest) extsdk.HTTPResponse {
	resp := env.send(ctx, req)
	resp.Error = env.redact(resp.Error)
	resp.Body = env.redactBytes(resp.Body)
	for k, v := range resp.Headers {
		resp.Headers[k] = env.redact(v)
	}
	return resp
}

// send checks the host on the URL as the module wrote it, then fills in the
// token. The placeholder may only sit in the path, query, headers or body:
// in the host or user info it would turn the host check into a way to read
// the key. The debug record keeps the placeholder, so the token is never
// captured.
func (env *callEnv) send(ctx context.Context, req extsdk.HTTPRequest) extsdk.HTTPResponse {
	env.requests++
	if env.requests > maxRequestsPerCall {
		return extsdk.HTTPResponse{Error: "too many requests in one call"}
	}
	if len(req.Body) > maxRequestBody {
		return extsdk.HTTPResponse{Error: "request body is too large"}
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodPut &&
		method != http.MethodPatch && method != http.MethodDelete {
		return extsdk.HTTPResponse{Error: "method " + method + " is not allowed"}
	}
	written, err := url.Parse(req.URL)
	if err != nil {
		return extsdk.HTTPResponse{Error: "invalid url"}
	}
	if strings.Contains(written.Host, extsdk.SecretPlaceholder) || written.User != nil {
		return extsdk.HTTPResponse{Error: "the token may only be used in the url path or query"}
	}
	if !env.allowed(written) {
		return extsdk.HTTPResponse{Error: fmt.Sprintf("host %s is not in allowed_hosts", written.Host)}
	}
	u, err := url.Parse(strings.ReplaceAll(req.URL, extsdk.SecretPlaceholder, url.QueryEscape(env.token)))
	if err != nil || u.Host != written.Host {
		return extsdk.HTTPResponse{Error: "invalid url"}
	}
	if env.limiter != nil {
		if err := env.limiter.Wait(ctx); err != nil {
			return extsdk.HTTPResponse{Error: "cancelled"}
		}
	}
	body := bytes.ReplaceAll(req.Body, []byte(extsdk.SecretPlaceholder), []byte(env.token))
	hreq, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return extsdk.HTTPResponse{Error: "invalid request"}
	}
	for k, v := range req.Headers {
		hreq.Header.Set(k, strings.ReplaceAll(v, extsdk.SecretPlaceholder, env.token))
	}

	start := time.Now()
	resp, err := env.httpClient().Do(hreq)
	if err != nil {
		msg := env.redact(err.Error())
		env.record(ctx, method, req, 0, nil, msg, time.Since(start))
		return extsdk.HTTPResponse{Error: msg}
	}
	defer resp.Body.Close()
	env.lastStatus = resp.StatusCode
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		msg := env.redact(err.Error())
		env.record(ctx, method, req, resp.StatusCode, nil, msg, time.Since(start))
		return extsdk.HTTPResponse{Error: msg}
	}
	if len(respBody) > maxResponseBody {
		return extsdk.HTTPResponse{Error: "response is too large"}
	}
	env.record(ctx, method, req, resp.StatusCode, respBody, "", time.Since(start))
	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	return extsdk.HTTPResponse{Status: resp.StatusCode, Headers: headers, Body: respBody}
}

// httpClient is the runtime's client with redirects held to allowed_hosts,
// so an allowed host cannot forward the call anywhere else.
func (env *callEnv) httpClient() *http.Client {
	c := *env.client
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		if !env.allowed(r.URL) {
			return fmt.Errorf("redirect to host %s is not in allowed_hosts", r.URL.Host)
		}
		return nil
	}
	return &c
}

func (env *callEnv) record(ctx context.Context, method string, req extsdk.HTTPRequest, status int, respBody []byte, errMsg string, d time.Duration) {
	if env.rec == nil {
		return
	}
	env.rec.Record(ctx, llmdebug.Entry{
		Kind:         llmdebug.KindExtension,
		Provider:     Provider(env.extID),
		URL:          method + " " + req.URL,
		Status:       status,
		DurationMs:   d.Milliseconds(),
		Error:        errMsg,
		RequestBody:  string(req.Body),
		ResponseBody: env.redact(string(respBody)),
	})
}
