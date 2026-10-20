package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/signedtoken"
)

// Guarded tools change shared configuration or delete
// data. They run in two calls:
//
//  1. Without confirm_token: nothing changes. The tool returns a Preview of
//     what would happen and a confirm_token bound to the tool, the caller,
//     where the call came from (the built-in assistant or another client),
//     and the exact arguments.
//  2. With that token and the same arguments: the change runs.
//
// The token is an HMAC, not a database row, so any replica can verify it.
// The built-in chat never shows the token to the model: it hands it to the
// browser as a confirm card, so only a user click completes the call.

// confirmTTL is how long a preview stays confirmable.
const confirmTTL = 10 * time.Minute

// Confirm is embedded in the input of every guarded tool.
type Confirm struct {
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"leave empty on the first call. The first call changes nothing and returns a preview plus a confirm_token. Show the preview to the user; only after they approve, call again with the token and every other argument unchanged."`
}

func (c Confirm) confirmToken() string { return c.ConfirmToken }

type confirmable interface{ confirmToken() string }

// Preview describes what a guarded call would change.
type Preview struct {
	// Summary is one sentence: "Delete the palette “Ocean”."
	Summary string `json:"summary"`
	// Effects lists knock-on consequences: "used by 4 projects".
	Effects []string `json:"effects,omitempty"`
	// VerificationToken proves the assistant's check of an issue report to
	// the report form, which sends the report itself (openloc_report_issue
	// only).
	VerificationToken string `json:"verification_token,omitempty"`
}

// Guarded is the result of every guarded tool.
type Guarded[T any] struct {
	// Status is "needs_confirmation" (nothing changed yet) or "done".
	Status       string   `json:"status"`
	Preview      *Preview `json:"preview,omitempty"`
	ConfirmToken string   `json:"confirm_token,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Result       *T       `json:"result,omitempty"`
}

const (
	guardNeedsConfirmation = "needs_confirmation"
	guardDone              = "done"
)

// addGuardedTool registers a tool that previews before it acts. The spec is
// forced to requireWrite — a read-only token can neither preview nor confirm —
// unless it is allowReadOnly.
func addGuardedTool[In confirmable, Out any](
	s *Server,
	srv *sdk.Server,
	managerTier bool,
	spec toolSpec,
	preview func(context.Context, In) (Preview, error),
	fn func(context.Context, In) (Out, error),
) {
	if !spec.allowReadOnly {
		spec.requireWrite = true
	}
	name := spec.name
	addTool(s, srv, managerTier, spec, func(ctx context.Context, in In) (Guarded[Out], error) {
		args, err := canonicalArgs(in)
		if err != nil {
			return Guarded[Out]{}, fmt.Errorf("canonical args: %w", err)
		}
		if in.confirmToken() == "" {
			p, err := preview(ctx, in)
			if err != nil {
				return Guarded[Out]{}, err
			}
			exp := s.now().Add(confirmTTL)
			return Guarded[Out]{
				Status:       guardNeedsConfirmation,
				Preview:      &p,
				ConfirmToken: s.signConfirm(ctx, name, args, exp),
				ExpiresAt:    exp.UTC().Format(time.RFC3339),
			}, nil
		}
		if !s.verifyConfirm(ctx, name, args, in.confirmToken()) {
			return Guarded[Out]{}, invalidf("the confirmation is invalid, expired, or the arguments changed — " +
				"call again without confirm_token to get a fresh preview")
		}
		out, err := fn(ctx, in)
		if err != nil {
			return Guarded[Out]{}, err
		}
		return Guarded[Out]{Status: guardDone, Result: &out}, nil
	})
}

// canonicalArgs is the JSON of the input without confirm_token. encoding/json
// writes struct fields in declaration order and map keys sorted, so the same
// arguments always give the same bytes.
func canonicalArgs(in any) ([]byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	delete(m, "confirm_token")
	return json.Marshal(m)
}

// confirmPayload is what a confirm token is signed for: the tool, the caller,
// the origin and the exact arguments. The origin is signed too: a preview
// another MCP client got never confirms through the built-in assistant, whose
// calls the report tool trusts with its check (fromBuiltInAssistant).
func confirmPayload(ctx context.Context, tool string, args []byte) string {
	tenantID, userID := scope(ctx)
	return fmt.Sprintf("%s|%d|%d|%t|%x", tool, tenantID, userID, fromBuiltInAssistant(ctx), sha256.Sum256(args))
}

func (s *Server) signConfirm(ctx context.Context, tool string, args []byte, exp time.Time) string {
	return signedtoken.Sign(s.confirmKey, exp, confirmPayload(ctx, tool, args))
}

func (s *Server) verifyConfirm(ctx context.Context, tool string, args []byte, token string) bool {
	return signedtoken.Verify(s.confirmKey, s.now(), token, confirmPayload(ctx, tool, args))
}

// DeriveConfirmKey turns an application secret into the confirm-token key,
// domain-separated so a confirm token can never be mistaken for a JWT.
func DeriveConfirmKey(secret []byte) []byte {
	return signedtoken.DeriveKey(secret, "openloc mcp confirm v1")
}
