package mcp

import "openloc/internal/auth"

// Per-token budget for the MCP endpoint: generous for interactive agent use
// (a model rarely issues more than a handful of tool calls per turn) while
// still bounding a runaway loop.
const (
	tokenRPS   = 5
	tokenBurst = 20
)

// NewTokenRateLimiter builds the per-credential limiter for /mcp. It is its
// own instance, so an agent and a REST integration on the same key do not
// starve each other.
func NewTokenRateLimiter() *auth.TokenRateLimiter {
	return auth.NewTokenRateLimiter("mcp", tokenRPS, tokenBurst)
}
