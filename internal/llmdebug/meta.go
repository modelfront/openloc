package llmdebug

// Recorder, Meta and the ctx plumbing live in their own file so tygo can
// exclude them (tygo.yaml exports only llmdebug.go — Entry + Kind consts —
// to web/src/types/llmdebug.ts).

import (
	"context"
	"reflect"
)

// Recorder is the capture hook held (nil-ably) by ai.HTTPTranslator, the
// extension runtime and pipeline.Service. *Ring and *Store implement it. A nil
// recorder means capture is off.
type Recorder interface {
	Record(ctx context.Context, e Entry)
}

// Meta is the pipeline context attached to ctx by callers that know which
// job/file/engine a provider call belongs to (the ai and extension packages
// deliberately don't).
type Meta struct {
	JobID   int64
	FileID  int64
	Project string // project name
	Step    string
	Engine  string
}

type metaKey struct{}

// WithMeta merges m over any Meta already in ctx — non-zero fields of m win,
// zero fields inherit. This lets an outer caller set FileID and an inner
// closure add Engine without either knowing about the other.
func WithMeta(ctx context.Context, m Meta) context.Context {
	prev := MetaFromContext(ctx)
	if m.JobID == 0 {
		m.JobID = prev.JobID
	}
	if m.FileID == 0 {
		m.FileID = prev.FileID
	}
	if m.Project == "" {
		m.Project = prev.Project
	}
	if m.Step == "" {
		m.Step = prev.Step
	}
	if m.Engine == "" {
		m.Engine = prev.Engine
	}
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFromContext returns the Meta attached via WithMeta, zero when absent.
func MetaFromContext(ctx context.Context) Meta {
	m, _ := ctx.Value(metaKey{}).(Meta)
	return m
}

// Multi fans one captured call out to several recorders. Its reason to exist is
// a narrow but confusing case: with both the dev ring and the prod Store wired,
// picking one would silently leave the /debug/llm viewer empty while capture
// looked enabled. A single recorder is returned as-is rather than wrapped.
//
// Nil recorders are skipped, INCLUDING typed nils — `Multi((*Ring)(nil))` gets
// an interface that is itself non-nil while holding a nil pointer, which a plain
// `r != nil` waves through and which then defeats the recorder==nil fast path at
// every capture site (or panics). Callers pass optional recorders straight in,
// so the check belongs here rather than in each caller. Reflection is fine: this
// runs once at startup, never per recorded call.
func Multi(recorders ...Recorder) Recorder {
	live := make([]Recorder, 0, len(recorders))
	for _, r := range recorders {
		if !isNil(r) {
			live = append(live, r)
		}
	}
	switch len(live) {
	case 0:
		return nil
	case 1:
		return live[0]
	}
	return multiRecorder(live)
}

// isNil reports whether r is either a nil interface or an interface holding a
// nil pointer.
func isNil(r Recorder) bool {
	if r == nil {
		return true
	}
	v := reflect.ValueOf(r)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

type multiRecorder []Recorder

// Record forwards to every recorder. Both implementations are non-blocking (the
// Ring takes a mutex, the Store does a non-blocking channel send), so fan-out
// costs no more than the slowest of them.
func (m multiRecorder) Record(ctx context.Context, e Entry) {
	for _, r := range m {
		r.Record(ctx, e)
	}
}
