package publicapi_test

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"
)

// undocumentedFields lists the fields of a JSON response body that the
// operation's response schema does not declare. The spec check alone accepts
// them, so a field added to a response type but not to the spec would ship
// without a line in the reference. An object schema with no properties, or with
// additionalProperties, is a map and declares no field names.
func undocumentedFields(op *openapi3.Operation, status int, body []byte) []string {
	resp := op.Responses.Status(status)
	if resp == nil || resp.Value == nil {
		return nil
	}
	media := resp.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil
	}
	var out []string
	walkFields(media.Schema, v, "", &out)
	sort.Strings(out)
	return out
}

func walkFields(ref *openapi3.SchemaRef, v any, at string, out *[]string) {
	if ref == nil || ref.Value == nil {
		return
	}
	switch v := v.(type) {
	case map[string]any:
		props, isMap := fieldsOf(ref.Value)
		if isMap {
			return
		}
		for key, val := range v {
			name := key
			if at != "" {
				name = at + "." + key
			}
			p, ok := props[key]
			if !ok {
				*out = append(*out, name)
				continue
			}
			walkFields(p, val, name, out)
		}
	case []any:
		for i, item := range v {
			walkFields(ref.Value.Items, item, at+"["+strconv.Itoa(i)+"]", out)
		}
	}
}

// fieldsOf merges the properties of a schema and its allOf parts. isMap is
// true when the schema names no fields: a free-form object or a map.
func fieldsOf(s *openapi3.Schema) (props map[string]*openapi3.SchemaRef, isMap bool) {
	props = map[string]*openapi3.SchemaRef{}
	var collect func(s *openapi3.Schema)
	collect = func(s *openapi3.Schema) {
		for k, p := range s.Properties {
			props[k] = p
		}
		for _, part := range s.AllOf {
			if part.Value != nil {
				collect(part.Value)
			}
		}
	}
	collect(s)
	hasAdditional := s.AdditionalProperties.Schema != nil || (s.AdditionalProperties.Has != nil && *s.AdditionalProperties.Has)
	return props, len(props) == 0 || hasAdditional || len(s.OneOf) > 0 || len(s.AnyOf) > 0
}
