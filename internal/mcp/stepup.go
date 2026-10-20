package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// errInsufficientScope is the write gate's refusal for a read-scoped OAuth
// grant. Unlike a read-only PAT, the grant can be widened: the client re-runs
// authorization asking for write, so the result carries a challenge saying so.
var errInsufficientScope = errors.New(writeRefused)

// writeRefused is the write gate's message to every caller it refuses.
const writeRefused = "writes require user confirmation or a token with write access"

// stepUpMetaKey is where ChatGPT reads a tool-level authorization challenge;
// the value is a list of WWW-Authenticate header values.
const stepUpMetaKey = "mcp/www_authenticate"

// stepUpMiddleware attaches challenge to every tools/call result refused with
// errInsufficientScope. The error has already become an isError result by
// then, and that result still carries it.
func stepUpMiddleware(challenge string) sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			res, err := next(ctx, method, req)
			if method != "tools/call" || err != nil {
				return res, err
			}
			if call, ok := res.(*sdk.CallToolResult); ok && errors.Is(call.GetError(), errInsufficientScope) {
				if call.Meta == nil {
					call.Meta = sdk.Meta{}
				}
				call.Meta[stepUpMetaKey] = []string{challenge}
			}
			return res, err
		}
	}
}
