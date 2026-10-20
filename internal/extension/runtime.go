package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"
	"golang.org/x/time/rate"

	"openloc/internal/llmdebug"
	"openloc/internal/netguard"
	"openloc/pkg/extsdk"
)

const (
	// callTimeout bounds one exported-function call, HTTP included.
	callTimeout = 2 * time.Minute
	// memoryPages caps guest memory (64 KiB pages → 256 MiB).
	memoryPages = 4096
	// cacheSize is how many compiled modules stay in memory.
	cacheSize = 32
)

// CallError is an error the extension itself returned, as opposed to a
// failure of the host. Its message is the extension's own text.
type CallError struct {
	Msg string
	// VendorStatus is the HTTP status of the last vendor response before the
	// error, 0 when the vendor did not answer.
	VendorStatus int
}

func (e *CallError) Error() string { return e.Msg }

// ErrNoFunction means the module does not export the called function.
var ErrNoFunction = errors.New("the extension does not export this function")

// Runtime compiles and runs extension modules. Safe for concurrent use.
type Runtime struct {
	httpClient *http.Client
	rec        llmdebug.Recorder

	mu    sync.Mutex
	cache map[string]*cacheEntry
	tick  uint64
	// limiters pace vendor requests per extension, key and rate, so calls
	// running at once share the manifest's requests_per_second.
	limiters map[string]*rate.Limiter
}

type cacheEntry struct {
	once     sync.Once
	compiled *extism.CompiledPlugin
	err      error
	used     uint64
	// refs counts running calls; an evicted entry is closed when it reaches 0.
	refs    int
	evicted bool
}

// NewRuntime returns a runtime. httpClient may be nil: the default client
// has a 30s timeout and refuses non-public addresses, since an admin sets the
// endpoint. rec may be nil (no debug capture).
func NewRuntime(httpClient *http.Client, rec llmdebug.Recorder) *Runtime {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{DialContext: netguard.DialContext(10 * time.Second), ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second},
		}
	}
	return &Runtime{httpClient: httpClient, rec: rec, cache: map[string]*cacheEntry{}, limiters: map[string]*rate.Limiter{}}
}

// Call is one invocation of an extension function.
type Call struct {
	Manifest extsdk.Manifest
	// Wasm loads the module; it is called only when the module with this
	// SHA256 is not compiled yet.
	Wasm     func(context.Context) ([]byte, error)
	SHA256   string
	Function string
	// Token fills extsdk.SecretPlaceholder. It never enters guest memory.
	Token   string
	BaseURL string
	Input   any
}

// Invoke runs c and decodes the JSON output into out (nil to ignore it).
func (rt *Runtime) Invoke(ctx context.Context, c Call, out any) error {
	entry, err := rt.acquire(ctx, c.SHA256, c.Wasm)
	if err != nil {
		return err
	}
	defer rt.release(entry)
	compiled := entry.compiled
	input, err := json.Marshal(c.Input)
	if err != nil {
		return fmt.Errorf("encode %s input: %w", c.Function, err)
	}
	env, err := newCallEnv(c, rt.httpClient, rt.rec, rt.limiterFor(c))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	ctx = withCallEnv(ctx, env)

	plugin, err := compiled.Instance(ctx, extism.PluginInstanceConfig{ModuleConfig: wazero.NewModuleConfig()})
	if err != nil {
		return fmt.Errorf("start extension %s: %w", c.Manifest.ID, err)
	}
	defer plugin.Close(ctx)
	if !plugin.FunctionExists(c.Function) {
		return fmt.Errorf("%w: %s", ErrNoFunction, c.Function)
	}
	rc, output, err := plugin.CallWithContext(ctx, c.Function, input)
	if ctx.Err() != nil {
		return fmt.Errorf("extension %s: %s timed out: %w", c.Manifest.ID, c.Function, ctx.Err())
	}
	if err != nil || rc != 0 {
		msg := "failed"
		if err != nil {
			msg = err.Error()
		}
		return &CallError{Msg: env.redact(msg), VendorStatus: env.lastStatus}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(output, out); err != nil {
		return &CallError{Msg: fmt.Sprintf("%s returned invalid JSON: %v", c.Function, err)}
	}
	return nil
}

// limiterFor returns the limiter shared by every call of c's extension with
// c's key at the manifest's requests_per_second; nil when it sets none. The
// key is held only as a hash.
func (rt *Runtime) limiterFor(c Call) *rate.Limiter {
	rps := c.Manifest.Limits.RequestsPerSecond
	if rps <= 0 {
		return nil
	}
	sum := sha256.Sum256([]byte(c.Token))
	key := c.Manifest.ID + "\x00" + hex.EncodeToString(sum[:]) + "\x00" + strconv.Itoa(rps)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	l, ok := rt.limiters[key]
	if !ok {
		l = rate.NewLimiter(rate.Limit(rps), 1)
		rt.limiters[key] = l
	}
	return l
}

// acquire returns the compiled module for sha, compiling it once, and holds
// it until release.
func (rt *Runtime) acquire(ctx context.Context, sha string, load func(context.Context) ([]byte, error)) (*cacheEntry, error) {
	if sha == "" {
		return nil, errors.New("extension module has no digest")
	}
	rt.mu.Lock()
	rt.tick++
	e, ok := rt.cache[sha]
	if !ok {
		e = &cacheEntry{used: rt.tick}
		rt.cache[sha] = e
		rt.evictLocked()
	}
	e.used = rt.tick
	e.refs++
	rt.mu.Unlock()

	e.once.Do(func() {
		wasm, err := load(ctx)
		if err != nil {
			e.err = err
			return
		}
		manifest := extism.Manifest{
			Wasm:    []extism.Wasm{extism.WasmData{Data: wasm, Hash: sha}},
			Memory:  &extism.ManifestMemory{MaxPages: memoryPages, MaxHttpResponseBytes: 0, MaxVarBytes: 1 << 20},
			Timeout: uint64(callTimeout.Milliseconds()),
		}
		// Compilation is detached from the request: the result is shared.
		e.compiled, e.err = extism.NewCompiledPlugin(context.Background(), manifest,
			extism.PluginConfig{EnableWasi: true}, []extism.HostFunction{httpHostFunction()})
	})
	if e.err != nil {
		rt.mu.Lock()
		if rt.cache[sha] == e {
			delete(rt.cache, sha)
		}
		e.refs--
		rt.mu.Unlock()
		return nil, fmt.Errorf("load extension module: %w", e.err)
	}
	return e, nil
}

func (rt *Runtime) release(e *cacheEntry) {
	rt.mu.Lock()
	e.refs--
	closeNow := e.evicted && e.refs == 0
	rt.mu.Unlock()
	if closeNow && e.compiled != nil {
		_ = e.compiled.Close(context.Background()) // best effort: frees compiled code
	}
}

// evictLocked drops the least recently used module past cacheSize. A module
// in use is closed by the last release.
func (rt *Runtime) evictLocked() {
	if len(rt.cache) <= cacheSize {
		return
	}
	var oldest string
	var min uint64
	for k, e := range rt.cache {
		if oldest == "" || e.used < min {
			oldest, min = k, e.used
		}
	}
	e := rt.cache[oldest]
	delete(rt.cache, oldest)
	e.evicted = true
	if e.refs == 0 && e.compiled != nil {
		_ = e.compiled.Close(context.Background()) // best effort: frees compiled code
	}
}
