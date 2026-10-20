// Package devseed builds dev-only demo projects whose jobs sit in chosen
// workflow states, for manual QA of the Tasks and Files tabs and the CAT
// editor's task row. Steps, tasks and job states are written directly — no
// pipeline runs — so nothing here is reachable from the server.
package devseed

import (
	"time"

	"openloc/internal/ptr"
)

// Step is one workflow step of a seeded project.
type Step struct {
	Name, Role, Type string
	// UsesRouter points the step at the engine-less seed router: AI and
	// hybrid steps need one, and with no engine nothing runs.
	UsesRouter bool
}

// FourSteps is the workflow both seeded projects use: a human step, an AI
// step, a hybrid step, and a human final step.
var FourSteps = []Step{
	{"Translation", "linguist", "human", false},
	{"Auto QE+APE", "linguist", "ai", true},
	{"Review", "reviewer", "hybrid", true},
	{"Sign-off", "reviewer", "human", false},
}

// StepPlan is one task: its status, who holds it (index into the tenant's
// people, -1 for nobody), its deadline from now, and for a finished task how
// long ago it finished.
type StepPlan struct {
	Status  string
	Who     int
	Due     *time.Duration
	DoneAgo time.Duration
	Reason  string
	// Note is the hand-off note: the comment the step before left for this one.
	Note string
}

// TextPlan is a job's translations, in segment order: the first Approved
// were confirmed by a person, the next AutoApproved approved by the AI step,
// the rest up to Filled hold machine translation awaiting review, and the
// remaining segments are untranslated.
type TextPlan struct {
	Approved, AutoApproved, Filled int
}

// Scenario is one job: a plan per workflow step, the job's own pipeline
// state, and its translations.
type Scenario struct {
	Name   string
	Steps  []StepPlan
	Status string // jobs.status — the app's pipeline_status
	Label  string // jobs.current_step
	ErrMsg string
	Done   int32 // translated / total, for a run in flight or stopped
	Total  int32
	Text   TextPlan
}

// Day is the unit the scenarios' dates are written in.
const Day = 24 * time.Hour

// In is a deadline d from now.
func In(d time.Duration) *time.Duration { return ptr.To(d) }

// Completed is a task person who finished ago.
func Completed(who int, ago time.Duration) StepPlan {
	return StepPlan{Status: "completed", Who: who, DoneAgo: ago}
}

// Active is a task in progress, due at due (nil: no deadline).
func Active(who int, due *time.Duration) StepPlan {
	return StepPlan{Status: "in_progress", Who: who, Due: due}
}

// Pending is a task not started, with nobody on it yet.
func Pending() StepPlan { return StepPlan{Status: "pending", Who: -1} }

// Planned is a task not started, assigned and dated in advance.
func Planned(who int, due *time.Duration) StepPlan {
	return StepPlan{Status: "pending", Who: who, Due: due}
}

// SentBack is a task a reviewer returned with a reason.
func SentBack(who int, reason string) StepPlan {
	return StepPlan{Status: "rejected", Who: who, Reason: reason}
}

// WithNote gives a task the comment the step before it left (the hand-off
// note the editor shows as a banner).
func (p StepPlan) WithNote(note string) StepPlan {
	p.Note = note
	return p
}

// Waiting is what the worker leaves on a job waiting for people.
const Waiting = "Pipeline complete"

// FailedAIStepError is the message a failed AI-step scenario carries.
const FailedAIStepError = "The AI engine returned an error (HTTP 503). Try again later."

// ReviewHandOffNote is the translator's comment the reviewing scenario's Review
// task receives.
const ReviewHandOffNote = "Two terms in section 2 changed in the glossary yesterday. Please check those segments first."

// SignOffHandOffNote is the reviewer's comment the signing-off scenario's last
// task receives.
const SignOffHandOffNote = "Two product names stay in English on purpose; the client asked for that."

// FailedMTError is the message a failed pre-translation scenario carries.
const FailedMTError = "MT engine rejected the API key."

// Workflow states on the four-step workflow, shared by the seeded projects.
var (
	// Finished: every task completed, the job delivered.
	Finished = Scenario{Name: "finished",
		Steps:  []StepPlan{Completed(0, 6*Day), Completed(-1, 6*Day), Completed(1, 5*Day), Completed(2, 4*Day)},
		Status: "completed", Label: "Workflow complete"}
	// Translating: the first task in progress, the rest not started.
	Translating = Scenario{Name: "translating",
		Steps:  []StepPlan{Active(0, In(3*Day)), Pending(), Pending(), Pending()},
		Status: "in_progress", Label: Waiting}
	// AIRunning: the AI step mid-run, which locks the editor.
	AIRunning = Scenario{Name: "AI step running",
		Steps:  []StepPlan{Completed(1, Day), Active(-1, nil), Pending(), Pending()},
		Status: "in_progress", Label: "AI step (QE+APE): 12 / 40", Done: 12, Total: 40}
	// AIFailed: the AI step's run failed, so its task is in error.
	AIFailed = Scenario{Name: "AI step failed",
		Steps:  []StepPlan{Completed(1, Day), Active(-1, nil), Pending(), Pending()},
		Status: "failed", Label: "AI step (QE+APE): 12 / 40", ErrMsg: FailedAIStepError}
	// AIFailedOnReview: the hybrid Review step's AI run failed, so the task
	// the reviewer works is in error until it is re-run or the AI is skipped.
	AIFailedOnReview = Scenario{Name: "AI failed on Review",
		Steps:  []StepPlan{Completed(0, 2*Day), Completed(-1, Day), Active(2, In(2*Day)), Pending()},
		Status: "failed", Label: "AI step (QE+APE): 12 / 40", ErrMsg: FailedAIStepError}
	// Reviewing: the hybrid Review step in progress after its AI run. The
	// translator's comment went to the AI step, which passed it on to Review.
	Reviewing = Scenario{Name: "reviewing",
		Steps: []StepPlan{Completed(0, 2*Day), Completed(-1, Day).WithNote(ReviewHandOffNote),
			Active(2, In(3*Day)).WithNote(ReviewHandOffNote), Pending()},
		Status: "in_progress", Label: "AI step complete"}
	// PretranslationFailed: MT failed on the first task, which is in error.
	PretranslationFailed = Scenario{Name: "pre-translation failed",
		Steps:  []StepPlan{Active(0, In(2*Day)), Pending(), Pending(), Pending()},
		Status: "failed", Label: "Translating", ErrMsg: FailedMTError}
	// SentBackToTranslation: Review returned the job, Translation reopened.
	SentBackToTranslation = Scenario{Name: "sent back",
		Steps: []StepPlan{Active(1, In(Day)), Completed(-1, 2*Day),
			SentBack(2, "The product name in the headline must stay in English."), Pending()},
		Status: "in_progress", Label: Waiting}
	// Stopped: a user stopped pre-translation part-way.
	Stopped = Scenario{Name: "stopped",
		Steps:  []StepPlan{Active(1, In(5*Day)), Pending(), Pending(), Pending()},
		Status: "stopped", Label: "Stopped — 120 / 400 segments translated; run Pre-translate again to resume",
		Done: 120, Total: 400}
	// SigningOff: the final step in progress, with the reviewer's comment.
	SigningOff = Scenario{Name: "signing off",
		Steps: []StepPlan{Completed(0, 4*Day), Completed(-1, 4*Day), Completed(2, 2*Day),
			Active(3, In(6*Day)).WithNote(SignOffHandOffNote)},
		Status: "in_progress", Label: Waiting}
)

// WithText is sc with translations planned.
func WithText(sc Scenario, t TextPlan) Scenario {
	sc.Text = t
	return sc
}

var corpus = []string{
	"Welcome to your new account.", "Here is how to get started in three steps.",
	"You can change your password at any time.", "Your order has been confirmed.",
	"We will email you when it ships.", "Track your delivery from your account page.",
	"Returns are free within 30 days.", "Contact our support team with any questions.",
	"What payment methods do you accept?", "Can I change my delivery address?",
	"How do I cancel my subscription?", "This release improves loading times.",
	"We fixed a problem with saved searches.", "Dark mode is now available on all pages.",
	"We collect only the data we need to run the service.", "You can ask us to delete your data.",
	"We never sell your personal information.", "This policy was last updated in September.",
	"Thank you for choosing us.",
}

// Sentences is n source sentences from the shared corpus, starting at from.
func Sentences(from, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = corpus[(from+i)%len(corpus)]
	}
	return out
}
