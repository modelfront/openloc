package publicapi

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadSpec(t *testing.T) *Spec {
	t.Helper()
	spec, err := LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	return spec
}

var validRoles = map[string]bool{"owner": true, "admin": true, "project_manager": true, "linguist": true}

// operations lists every (path, method, operation) in the spec, sorted.
func operations(doc *openapi3.T) []struct {
	Path, Method string
	Op           *openapi3.Operation
} {
	var out []struct {
		Path, Method string
		Op           *openapi3.Operation
	}
	for _, path := range doc.Paths.InMatchingOrder() {
		for method, op := range doc.Paths.Value(path).Operations() {
			out = append(out, struct {
				Path, Method string
				Op           *openapi3.Operation
			}{path, method, op})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path+out[i].Method < out[j].Path+out[j].Method
	})
	return out
}

// XRoles reads an operation's x-roles: the roles its route gate lets in.
func XRoles(op *openapi3.Operation) []string {
	raw, ok := op.Extensions["x-roles"]
	if !ok {
		return nil
	}
	b, _ := json.Marshal(raw)
	var roles []string
	_ = json.Unmarshal(b, &roles)
	return roles
}

// Every operation is documented the same way, so a reader and the
// spec-driven tests can rely on it.
func TestSpecLint(t *testing.T) {
	spec := loadSpec(t)
	seenIDs := map[string]bool{}
	ops := operations(spec.Doc)
	if len(ops) < 15 {
		t.Fatalf("only %d operations found; the walk did not see the spec", len(ops))
	}
	for _, o := range ops {
		name := o.Method + " " + o.Path
		op := o.Op
		if op.OperationID == "" {
			t.Errorf("%s: missing operationId", name)
		} else if seenIDs[op.OperationID] {
			t.Errorf("%s: duplicate operationId %q", name, op.OperationID)
		}
		seenIDs[op.OperationID] = true
		if op.Summary == "" {
			t.Errorf("%s: missing summary", name)
		}
		if len(op.Tags) != 1 {
			t.Errorf("%s: want exactly one tag, got %v", name, op.Tags)
		}
		roles := XRoles(op)
		if len(roles) == 0 {
			t.Errorf("%s: missing x-roles", name)
		}
		for _, r := range roles {
			if !validRoles[r] {
				t.Errorf("%s: x-roles names unknown role %q", name, r)
			}
		}
		for _, code := range []string{"401", "403", "429"} {
			if op.Responses.Value(code) == nil {
				t.Errorf("%s: missing %s response", name, code)
			}
		}
		if strings.Contains(o.Path, "{") && op.Responses.Value("404") == nil {
			t.Errorf("%s: has path parameters but no 404 response", name)
		}
		if o.Method != "GET" && o.Method != "DELETE" && op.Responses.Value("400") == nil && op.RequestBody != nil {
			t.Errorf("%s: takes a body but documents no 400 response", name)
		}
		for code, ref := range op.Responses.Map() {
			if code >= "400" && ref.Ref == "" {
				t.Errorf("%s: %s response must $ref a shared error response", name, code)
			}
		}
	}
}

func TestSpecMerge_RejectsDuplicates(t *testing.T) {
	base := []byte("paths:\n  /x: {}\ncomponents:\n  schemas:\n    Error: {type: object}\n")
	cases := map[string]string{
		"path defined twice":         "paths:\n  /x: {}\n",
		"schema defined twice":       "components:\n  schemas:\n    Error: {type: string}\n",
		"top-level key outside base": "info: {title: other}\n",
	}
	for name, part := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"openapi/base.yaml":  {Data: base},
				"openapi/block.yaml": {Data: []byte(part)},
			}
			if _, err := mergeSpecFiles(fsys); err == nil {
				t.Fatal("merge accepted it")
			}
		})
	}
}

func TestSpecMerge_AddsPathsAndSchemas(t *testing.T) {
	fsys := fstest.MapFS{
		"openapi/base.yaml": {Data: []byte("paths:\n  /a: {}\ncomponents:\n  schemas:\n    A: {type: object}\n")},
		"openapi/b.yaml":    {Data: []byte("paths:\n  /b: {}\ncomponents:\n  schemas:\n    B: {type: object}\n  parameters:\n    P: {name: p, in: query}\n")},
	}
	got, err := mergeSpecFiles(fsys)
	if err != nil {
		t.Fatal(err)
	}
	paths := got["paths"].(map[string]any)
	schemas := got["components"].(map[string]any)["schemas"].(map[string]any)
	params := got["components"].(map[string]any)["parameters"].(map[string]any)
	if paths["/a"] == nil || paths["/b"] == nil || schemas["A"] == nil || schemas["B"] == nil || params["P"] == nil {
		t.Fatalf("merge lost an entry: %v", got)
	}
}

// The JSON served at /openapi.json is the validated document, so a client
// generator gets exactly what the tests checked.
func TestSpecJSON_RoundTrips(t *testing.T) {
	spec := loadSpec(t)
	doc, err := openapi3.NewLoader().LoadFromData(spec.JSON)
	if err != nil {
		t.Fatalf("served JSON does not load: %v", err)
	}
	if len(operations(doc)) != len(operations(spec.Doc)) {
		t.Fatal("served JSON lost operations")
	}
}

// Operations carry their position in the spec files, so the reference lists
// them in the order the author wrote them rather than sorted by path.
func TestSpecMerge_StampsOperationOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"openapi/base.yaml": {Data: []byte("paths:\n  /z:\n    get: {}\n")},
		"openapi/b.yaml":    {Data: []byte("paths:\n  /m:\n    post: {}\n    get: {}\n  /a:\n    parameters: []\n    delete: {}\n")},
	}
	got, err := mergeSpecFiles(fsys)
	if err != nil {
		t.Fatal(err)
	}
	paths := got["paths"].(map[string]any)
	order := func(path, method string) any { return paths[path].(map[string]any)[method].(map[string]any)["x-order"] }
	want := map[[2]string]int{{"/z", "get"}: 0, {"/m", "post"}: 1, {"/m", "get"}: 2, {"/a", "delete"}: 3}
	for k, v := range want {
		if got := order(k[0], k[1]); got != v {
			t.Errorf("%s %s x-order = %v, want %d", k[1], k[0], got, v)
		}
	}
}
