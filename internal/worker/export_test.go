package worker

import "context"

// SetLastAttemptForTest pins isLastAttempt for the external test package,
// whose integration tests cannot build asynq's retry-count context. Call the
// returned func to restore it.
func SetLastAttemptForTest(last bool) (restore func()) {
	orig := isLastAttempt
	isLastAttempt = func(context.Context) bool { return last }
	return func() { isLastAttempt = orig }
}

// SetRetryForTest pins isRetry the same way: true makes the next delivery read
// as a retry of a failed attempt.
func SetRetryForTest(retry bool) (restore func()) {
	orig := isRetry
	isRetry = func(context.Context) bool { return retry }
	return func() { isRetry = orig }
}
