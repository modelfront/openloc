package mcp

import (
	"context"
	"testing"
	"time"

	"openloc/internal/auth"
	"openloc/internal/signedtoken"
)

// A confirm token works only where its preview came from: a preview another
// MCP client got cannot be confirmed through the built-in assistant (the
// chat's confirm route), which the report tool trusts with its check.
func TestConfirmTokenKeepsItsOrigin(t *testing.T) {
	s := &Server{confirmKey: signedtoken.RandomKey(), now: time.Now}
	external := auth.WithClaims(context.Background(), &auth.Claims{TenantID: 1, UserID: 2, Role: "owner"})
	inApp := context.WithValue(external, builtInAssistantKey{}, true)
	args := []byte(`{"title":"Grid is empty","verification":"verified"}`)
	exp := time.Now().Add(time.Minute)

	fromClient := s.signConfirm(external, toolReportIssue, args, exp)
	if !s.verifyConfirm(external, toolReportIssue, args, fromClient) {
		t.Fatal("a client's token does not confirm on that client")
	}
	if s.verifyConfirm(inApp, toolReportIssue, args, fromClient) {
		t.Error("a client's token confirms through the built-in assistant")
	}
	fromChat := s.signConfirm(inApp, toolReportIssue, args, exp)
	if !s.verifyConfirm(inApp, toolReportIssue, args, fromChat) {
		t.Error("the assistant's token does not confirm through the assistant")
	}
}
