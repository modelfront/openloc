package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectInProcess opens an MCP client session against the same prebuilt
// per-role servers that serve /mcp, over an in-memory transport.
// This is how the built-in chat reaches the tool surface without a network
// hop or a PAT.
//
// Identity: the SDK derives tool-handler contexts from the context passed to
// server Connect (jsonrpc2.NewConnection captures it), so ctx MUST already
// carry auth.Claims — the same handlers that trust auth.Middleware on the
// HTTP path will read them via auth.ClaimsFromContext. Passing a claims-less
// context doesn't fail open: requireManager tools deny, and tenant-scoped
// queries see tenant 0. TestInProcessClaimsPropagation pins this contract.
//
// The role tier deliberately reuses isManagerRole so chat and /mcp can never
// disagree about which tools a role sees. Tool calls on the session know they
// come from the built-in assistant (fromBuiltInAssistant).
//
// Close the returned session when the conversation turn is done; it tears
// down both ends of the pipe.
func (s *Server) ConnectInProcess(ctx context.Context, role string) (*sdk.ClientSession, error) {
	ctx = context.WithValue(ctx, builtInAssistantKey{}, true)
	srv := s.linguist
	if isManagerRole(role) {
		srv = s.manager
	}
	// Symmetric transports; server side must connect first (the client
	// initializes the MCP session during Connect).
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		return nil, fmt.Errorf("mcp: in-process server connect: %w", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "openloc-chat", Version: s.deps.Version}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: in-process client connect: %w", err)
	}
	return cs, nil
}

type builtInAssistantKey struct{}

// fromBuiltInAssistant reports a tool call from OpenLoc's own chat, which
// follows the app's playbooks; any other MCP client does not.
func fromBuiltInAssistant(ctx context.Context) bool {
	on, _ := ctx.Value(builtInAssistantKey{}).(bool)
	return on
}
