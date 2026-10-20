package aiprompt

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
)

// Shared by the REST handler and the customer API: create and edit in one
// transaction, with the built-in and prompt-type rules. Caller mistakes come
// back as ValidationError, an unknown or foreign prompt as pgx.ErrNoRows.

// inTx runs fn in a transaction on pool, or on q directly when pool is nil
// (unit tests).
func inTx(ctx context.Context, q db.Querier, pool *pgxpool.Pool, fn func(q db.Querier) error) error {
	if pool == nil {
		return fn(q)
	}
	return database.InTx(ctx, pool, func(_ pgx.Tx, txq db.Querier) error { return fn(txq) })
}

// CreateTx validates and creates a prompt with its language additions, and
// reads the additions back as stored in the same transaction, so a failed
// read saves nothing.
func CreateTx(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID int64, in Input) (db.AiPrompt, []LanguageAddition, error) {
	var prompt db.AiPrompt
	var additions []LanguageAddition
	err := inTx(ctx, q, pool, func(q db.Querier) error {
		var err error
		if prompt, err = Create(ctx, q, tenantID, in); err != nil {
			return err
		}
		additions, err = additionsFor(ctx, q, prompt.ID)
		return err
	})
	if err != nil {
		return db.AiPrompt{}, nil, err
	}
	return prompt, additions, nil
}

// Edit replaces a prompt's text, outputs, context and additions, and reads the
// additions back as stored in the same transaction, so a failed read changes
// nothing. A built-in prompt is read-only, and a prompt keeps the type it was
// created with.
func Edit(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, id int64, in Input) (db.AiPrompt, []LanguageAddition, error) {
	existing, err := q.GetAIPrompt(ctx, db.GetAIPromptParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.AiPrompt{}, nil, err
	}
	if existing.IsDefault {
		return db.AiPrompt{}, nil, &ValidationError{Msg: "default prompts are read-only — create a new prompt to customize"}
	}
	if Mode(existing) != ModeForFlags(in.OutStatus, in.OutText, in.OutComments) {
		return db.AiPrompt{}, nil, &ValidationError{Msg: "prompt type cannot be changed after creation"}
	}
	if err := Validate(&in); err != nil {
		return db.AiPrompt{}, nil, err
	}
	var prompt db.AiPrompt
	var additions []LanguageAddition
	err = inTx(ctx, q, pool, func(q db.Querier) error {
		var err error
		if prompt, err = Update(ctx, q, tenantID, id, in); err != nil {
			return err
		}
		additions, err = additionsFor(ctx, q, prompt.ID)
		return err
	})
	if err != nil {
		return db.AiPrompt{}, nil, err
	}
	return prompt, additions, nil
}

// AdditionsOf returns the language additions of every prompt of the tenant,
// keyed by prompt id.
func AdditionsOf(ctx context.Context, q db.Querier, tenantID int64) (map[int64][]LanguageAddition, error) {
	rows, err := q.ListAIPromptLanguageAdditions(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list prompt language additions: %w", err)
	}
	out := make(map[int64][]LanguageAddition, len(rows))
	for _, r := range rows {
		out[r.PromptID] = append(out[r.PromptID], toAddition(r))
	}
	return out, nil
}

// additionsFor returns one prompt's language additions in order.
func additionsFor(ctx context.Context, q db.Querier, promptID int64) ([]LanguageAddition, error) {
	rows, err := q.ListAIPromptLanguageAdditionsForPrompt(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("list prompt language additions: %w", err)
	}
	out := make([]LanguageAddition, len(rows))
	for i, r := range rows {
		out[i] = toAddition(r)
	}
	return out, nil
}

func toAddition(r db.AiPromptLanguageAddition) LanguageAddition {
	return LanguageAddition{TargetLanguages: r.TargetLanguages, Text: r.AdditionText}
}
