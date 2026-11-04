package vendoridle

// SetBatch makes a test's batches small, so a few rows fill one.
func (s *Sweeper) SetBatch(n int32) { s.batch = n }
