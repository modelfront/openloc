package publicapi

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// The spec is hand-written: base.yaml carries info, security and the shared
// components, and each other file adds one block's paths and schemas. Every
// $ref is local (#/components/...), so the files merge into one document.
//
//go:embed openapi/*.yaml
var specFS embed.FS

// Spec is the loaded, validated OpenAPI document and its JSON encoding.
type Spec struct {
	Doc  *openapi3.T
	JSON []byte
	etag string
}

// LoadSpec merges the embedded spec files, validates the result and encodes
// it once. An invalid spec is a build-time mistake, so the caller refuses to
// start on an error.
func LoadSpec() (*Spec, error) {
	merged, err := mergeSpecFiles(specFS)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("publicapi: encode spec: %w", err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		return nil, fmt.Errorf("publicapi: load spec: %w", err)
	}
	if err := doc.Validate(context.Background(), openapi3.EnableExamplesValidation()); err != nil {
		return nil, fmt.Errorf("publicapi: invalid spec: %w", err)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("publicapi: encode spec: %w", err)
	}
	sum := sha256.Sum256(out)
	return &Spec{Doc: doc, JSON: out, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}, nil
}

// mergeSpecFiles starts from base.yaml and adds every other file's paths and
// components. A path, component or method defined twice is an error, so two
// blocks cannot silently overwrite each other.
func mergeSpecFiles(fsys fs.FS) (map[string]any, error) {
	order := 0
	base, err := readYAML(fsys, "openapi/base.yaml", &order)
	if err != nil {
		return nil, err
	}
	names, err := fs.Glob(fsys, "openapi/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "openapi/base.yaml" {
			continue
		}
		part, err := readYAML(fsys, name, &order)
		if err != nil {
			return nil, err
		}
		for key := range part {
			if key != "paths" && key != "components" {
				return nil, fmt.Errorf("publicapi: %s: only paths and components may be set outside base.yaml (found %q)", name, key)
			}
		}
		if err := mergeInto(base, "paths", part["paths"], name, 1); err != nil {
			return nil, err
		}
		// components → kind (schemas, parameters, …) → name.
		if err := mergeInto(base, "components", part["components"], name, 2); err != nil {
			return nil, err
		}
	}
	return base, nil
}

// mergeInto adds src under dst[key], descending depth levels of maps before
// a duplicate key becomes an error.
func mergeInto(dst map[string]any, key string, src any, file string, depth int) error {
	if src == nil {
		return nil
	}
	srcMap, ok := src.(map[string]any)
	if !ok {
		return fmt.Errorf("publicapi: %s: %s must be a map", file, key)
	}
	dstMap, _ := dst[key].(map[string]any)
	if dstMap == nil {
		dstMap = map[string]any{}
		dst[key] = dstMap
	}
	for k, v := range srcMap {
		existing, taken := dstMap[k]
		if !taken {
			dstMap[k] = v
			continue
		}
		if depth <= 1 {
			return fmt.Errorf("publicapi: %s: %s.%s is already defined", file, key, k)
		}
		if _, ok := existing.(map[string]any); !ok {
			return fmt.Errorf("publicapi: %s: %s.%s is already defined", file, key, k)
		}
		if err := mergeInto(dstMap, k, v, file, depth-1); err != nil {
			return err
		}
	}
	return nil
}

// readYAML parses one spec file. Each operation gets `x-order`, its position
// across the files as written (base.yaml first, then by file name), because
// the JSON encoding sorts paths and the reference pages list operations in
// the order the author chose.
func readYAML(fsys fs.FS, name string, order *int) (map[string]any, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("publicapi: read %s: %w", name, err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("publicapi: parse %s: %w", name, err)
	}
	var out map[string]any
	if err := node.Decode(&out); err != nil {
		return nil, fmt.Errorf("publicapi: parse %s: %w", name, err)
	}
	paths, _ := out["paths"].(map[string]any)
	for _, path := range mappingKeys(mappingValue(node.Content[0], "paths")) {
		item, _ := paths[path].(map[string]any)
		for _, method := range mappingKeys(mappingValue(mappingValue(node.Content[0], "paths"), path)) {
			if op, ok := item[method].(map[string]any); ok && httpMethods[method] {
				op["x-order"] = *order
				*order++
			}
		}
	}
	return out, nil
}

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "patch": true, "delete": true, "head": true, "options": true}

// mappingValue returns the value node of key in a YAML mapping, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingKeys lists a YAML mapping's keys in document order.
func mappingKeys(m *yaml.Node) []string {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	keys := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	return keys
}

// ServeHTTP serves the spec as JSON. Any role may read it.
func (s *Spec) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("ETag", s.etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == s.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(s.JSON)
}
