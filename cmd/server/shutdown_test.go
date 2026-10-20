package main

import (
	"context"
	"os"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"
)

// A step runs to its own limit unless the shared deadline comes first, so the
// steps together never outlast the budget.
func TestStepContext_EndsAtTheEarlierOfItsLimitAndTheDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Second)

	short, cancel := stepContext(deadline, 100*time.Millisecond)
	defer cancel()
	if end, _ := short.Deadline(); !end.Before(deadline) {
		t.Errorf("a step whose limit ends first ends at %s, after the deadline %s", end, deadline)
	}

	long, cancel := stepContext(deadline, time.Minute)
	defer cancel()
	if end, _ := long.Deadline(); !end.Equal(deadline) {
		t.Errorf("a step whose limit outlasts the deadline ends at %s, want the deadline %s", end, deadline)
	}
}

// testLimits add up to more than their budget: the worker drain, the LLM
// debug flush and the HTTP drain alone would take 1500 ms of 1500, so the
// shared deadline has to cut the HTTP drain for the broker and the reports to
// get their turn. The drain starts 600 ms before it is cut, so a late timer
// under load still leaves it time of its own.
var testLimits = shutdownLimits{
	budget:        1500 * time.Millisecond,
	workers:       300 * time.Millisecond,
	llmDebugFlush: 300 * time.Millisecond,
	httpGrace:     900 * time.Millisecond,
	brokerClose:   200 * time.Millisecond,
	reportFlush:   100 * time.Millisecond,
}

// startLog records when each step started, relative to the shutdown's start.
type startLog struct {
	mu     sync.Mutex
	start  time.Time
	names  []string
	starts map[string]time.Duration
}

func newStartLog() *startLog {
	return &startLog{start: time.Now(), starts: map[string]time.Duration{}}
}

func (l *startLog) step(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.names = append(l.names, name)
	l.starts[name] = time.Since(l.start)
}

// runShutdown runs the shutdown with its budget ending at end, and fails the
// test when it is still running well past end.
func runShutdown(t *testing.T, end time.Time, l shutdownLimits, s shutdownSteps) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- shutdownBy(end, l, s) }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Until(end) + 500*time.Millisecond):
		t.Fatalf("shutdown still running %s past the end of its %s budget", -time.Until(end), l.budget)
	}
	return nil
}

// The worker drain and the pool close never return (Redis gone, a wedged
// handler keeping its connection), and every other step that waits does so
// until its context ends. The shutdown still runs every step in order, cuts
// the HTTP drain so the broker and the reports start on time, sends the
// reports before the pool closes, ends within its budget and returns the
// drain's error.
func TestShutdown_RunsEveryStepInOrderWithinItsBudget(t *testing.T) {
	wedged := make(chan struct{})
	defer close(wedged)
	log := newStartLog()
	var httpTime time.Duration
	var httpEnd, brokerEnd time.Time
	steps := shutdownSteps{
		stopWorkers: func() {
			log.step("stopWorkers")
			<-wedged
		},
		flushLLMDebug: func(ctx context.Context) {
			log.step("flushLLMDebug")
			<-ctx.Done()
		},
		drainHTTP: func(ctx context.Context) error {
			log.step("drainHTTP")
			httpEnd, _ = ctx.Deadline()
			httpTime = time.Until(httpEnd)
			<-ctx.Done()
			return ctx.Err()
		},
		closeBroker: func(ctx context.Context) {
			log.step("closeBroker")
			brokerEnd, _ = ctx.Deadline()
			<-ctx.Done()
		},
		flushReports: func() { log.step("flushReports") },
		closePool: func() {
			log.step("closePool")
			<-wedged
		},
	}

	end := log.start.Add(testLimits.budget)
	if err := runShutdown(t, end, testLimits, steps); err == nil {
		t.Error("the HTTP drain's timeout was not returned")
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	want := []string{"stopWorkers", "flushLLMDebug", "drainHTTP", "closeBroker", "flushReports", "closePool"}
	if !slices.Equal(log.names, want) {
		t.Fatalf("steps ran as %v, want %v", log.names, want)
	}
	if httpTime <= 0 {
		t.Error("the HTTP drain got no time of its own")
	}
	// Each step waits out the one before it: timers never fire early, so a
	// start before its earliest moment means a wait was cut short.
	l := testLimits
	drainBy := l.budget - l.reportFlush - l.brokerClose
	for name, earliest := range map[string]time.Duration{
		"flushLLMDebug": l.workers,
		"drainHTTP":     l.workers + l.llmDebugFlush,
		"closeBroker":   drainBy,
		"flushReports":  l.budget - l.reportFlush,
	} {
		if got := log.starts[name]; got < earliest {
			t.Errorf("%s started at %s, before %s: the step before it was not waited for", name, got, earliest)
		}
	}
	// The deadlines, unlike start times, do not move with a late timer.
	if want := end.Add(-l.reportFlush - l.brokerClose); !httpEnd.Equal(want) {
		t.Errorf("the HTTP drain ends %s before the budget, want %s: it was not cut for the broker and the reports", end.Sub(httpEnd), l.reportFlush+l.brokerClose)
	}
	if want := end.Add(-l.reportFlush); !brokerEnd.Equal(want) {
		t.Errorf("the broker close ends %s before the budget, want %s, kept for the reports", end.Sub(brokerEnd), l.reportFlush)
	}
}

// A reporting service that never answers does not hold the exit past the
// budget.
func TestShutdown_ASilentReportingServiceDoesNotHoldTheExit(t *testing.T) {
	silent := make(chan struct{})
	defer close(silent)
	quick := func(context.Context) {}
	steps := shutdownSteps{
		stopWorkers:   func() {},
		flushLLMDebug: quick,
		drainHTTP:     func(context.Context) error { return nil },
		closeBroker:   quick,
		flushReports:  func() { <-silent },
		closePool:     func() {},
	}
	if err := runShutdown(t, time.Now().Add(testLimits.budget), testLimits, steps); err != nil {
		t.Errorf("shutdown returned %v for a drain that finished", err)
	}
}

// The wait for the workers outlasts asynq's own timeout, after which it
// requeues the tasks it gave up on: leaving first leaves those tasks running
// in Redis until their lease ends, each charged a retry.
func TestShutdown_WaitsForTheWorkersPastTheirOwnTimeout(t *testing.T) {
	if margin := serverShutdownLimits.workers - asynqShutdownTimeout; margin < time.Second {
		t.Errorf("the wait for the workers ends %s after asynq's own %s timeout, want at least 1s for its requeue", margin, asynqShutdownTimeout)
	}
}

// exitMargin is left between the budget and SIGKILL for the process to exit.
const exitMargin = time.Second

// The budget fits the time the self-host stack's runtime waits before SIGKILL,
// and leaves the steps that share it more than the worker drain and what is
// reserved for the broker and the error reports.
func TestShutdownBudget_FitsTheStopGracePeriod(t *testing.T) {
	compose, err := os.ReadFile("../../deploy/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`stop_grace_period:\s*(\S+)`).FindSubmatch(compose)
	if m == nil {
		t.Fatal("deploy/docker-compose.yml sets no stop_grace_period")
	}
	grace, err := time.ParseDuration(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	l := serverShutdownLimits
	if l.budget+exitMargin > grace {
		t.Errorf("shutdown budget %s plus %s to exit exceeds stop_grace_period %s", l.budget, exitMargin, grace)
	}
	if reserved := l.workers + l.brokerClose + l.reportFlush; reserved >= l.budget {
		t.Errorf("the workers, the broker and the error reports take %s, nothing of the %s budget", reserved, l.budget)
	}
}
