package publicapi

import (
	"encoding/json"

	"github.com/getkin/kin-openapi/openapi3"
)

// XRoles reads an operation's x-roles: the roles its route gate lets in.
func XRoles(op *openapi3.Operation) []string {
	var roles []string
	extension(op, "x-roles", &roles)
	return roles
}

// XOrder reads an operation's x-order, its position in the spec files.
func XOrder(op *openapi3.Operation) int {
	order := int(^uint(0) >> 1)
	extension(op, "x-order", &order)
	return order
}

// XHidden reports whether an operation is left out of the endpoint reference.
func XHidden(op *openapi3.Operation) bool {
	var hidden bool
	extension(op, "x-hidden", &hidden)
	return hidden
}

// extension decodes an operation extension into out; a missing or malformed
// one leaves out unchanged.
func extension(op *openapi3.Operation, key string, out any) {
	raw, ok := op.Extensions[key]
	if !ok {
		return
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return
	}
	if err := json.Unmarshal(b, out); err != nil {
		return
	}
}
