package taskedit

import (
	"testing"

	"openloc/internal/auth"
)

func TestDecide(t *testing.T) {
	inProgress := State{HasWorkflow: true, JobStatus: "in_progress", JobCurrentStep: "pre-translation complete", ActiveID: 7}
	completed := State{HasWorkflow: true, JobStatus: "completed", JobCurrentStep: "feedback sent", LastCompletedID: 9}
	with := func(st State, f func(*State)) State { f(&st); return st }

	cases := []struct {
		name    string
		st      State
		role    string
		opts    Opts
		want    verdict
		wantErr string
	}{
		{name: "no workflow", st: State{}, role: auth.RoleLinguist, want: allow},
		{name: "in progress, linguist", st: inProgress, role: auth.RoleLinguist, want: allow},
		{name: "in progress, manager", st: inProgress, role: auth.RoleProjectManager, want: allow},
		{name: "pre-translation running on a hybrid step stays editable",
			st:   with(inProgress, func(s *State) { s.ActiveIsAIStep = true; s.JobCurrentStep = "Running TM lookup + MT" }),
			role: auth.RoleLinguist, want: allow},
		{name: "AI step running",
			st:   with(inProgress, func(s *State) { s.ActiveIsAIStep = true; s.JobCurrentStep = "AI step (QE): 3 / 9" }),
			role: auth.RoleOwner, wantErr: CodeAIStepRunning},
		{name: "AI step finished",
			st:   with(inProgress, func(s *State) { s.ActiveIsAIStep = true; s.JobCurrentStep = "AI step complete" }),
			role: auth.RoleLinguist, want: allow},
		{name: "pre-translation running on a Pre-translation step",
			st:   with(inProgress, func(s *State) { s.ActiveStepType = "pretranslate"; s.JobCurrentStep = "Running TM lookup + MT" }),
			role: auth.RoleOwner, wantErr: CodePretranslationRunning},
		{name: "queued pre-translation on a Pre-translation step",
			st: with(inProgress, func(s *State) {
				s.ActiveStepType = "pretranslate"
				s.JobStatus = "queued"
				s.JobCurrentStep = "Queued for pre-translation"
			}),
			role: auth.RoleOwner, wantErr: CodePretranslationRunning},
		{name: "reopened Pre-translation step is editable",
			st:   with(inProgress, func(s *State) { s.ActiveStepType = "pretranslate"; s.JobCurrentStep = "Task reopen complete" }),
			role: auth.RoleProjectManager, want: allow},
		{name: "completed Pre-translation step, manager reopens",
			st:   with(completed, func(s *State) { s.LastCompletedType = "pretranslate" }),
			role: auth.RoleProjectManager, want: reopen},
		{name: "completed Pre-translation step, linguist refused",
			st:   with(completed, func(s *State) { s.LastCompletedType = "pretranslate" }),
			role: auth.RoleLinguist, wantErr: CodeTaskCompleted},
		{name: "errored AI step, manager",
			st:   with(inProgress, func(s *State) { s.JobStatus = "failed"; s.JobCurrentStep = "Running AI step" }),
			role: auth.RoleOwner, wantErr: CodeTaskErrored},
		{name: "errored pre-translation",
			st:   with(inProgress, func(s *State) { s.JobStatus = "failed"; s.JobCurrentStep = "MT: 3 / 9 segments" }),
			role: auth.RoleOwner, wantErr: CodeTaskErrored},
		{name: "errored pre-translation, Apply TM/MT",
			st:   with(inProgress, func(s *State) { s.JobStatus = "failed"; s.JobCurrentStep = "MT: 3 / 9 segments" }),
			role: auth.RoleLinguist, opts: Opts{AllowPretranslationError: true}, want: allow},
		{name: "errored AI step, Apply TM/MT",
			st:   with(inProgress, func(s *State) { s.JobStatus = "failed"; s.JobCurrentStep = "Running AI step" }),
			role: auth.RoleOwner, opts: Opts{AllowPretranslationError: true}, wantErr: CodeTaskErrored},
		{name: "completed, owner reopens", st: completed, role: auth.RoleOwner, want: reopen},
		{name: "completed, admin reopens", st: completed, role: auth.RoleAdmin, want: reopen},
		{name: "completed, project manager reopens", st: completed, role: auth.RoleProjectManager, want: reopen},
		{name: "completed, linguist refused", st: completed, role: auth.RoleLinguist, wantErr: CodeTaskCompleted},
		{name: "completed, manager without reopen (MCP)", st: completed, role: auth.RoleOwner,
			opts: Opts{NoReopen: true}, wantErr: CodeTaskCompleted},
		{name: "completed but failed",
			st: with(completed, func(s *State) { s.JobStatus = "failed" }), role: auth.RoleOwner, wantErr: CodeTaskErrored},
		{name: "nothing started", st: State{HasWorkflow: true, JobStatus: "created"}, role: auth.RoleOwner,
			wantErr: CodeTaskNotStarted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, lerr := decide(c.st, c.role, c.opts)
			if c.wantErr != "" {
				if lerr == nil || lerr.Code != c.wantErr {
					t.Fatalf("decide = (%v, %v), want refusal %q", v, lerr, c.wantErr)
				}
				return
			}
			if lerr != nil {
				t.Fatalf("decide refused with %q, want verdict %v", lerr.Code, c.want)
			}
			if v != c.want {
				t.Fatalf("verdict = %v, want %v", v, c.want)
			}
		})
	}
}
