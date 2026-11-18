package check

// WatchLog remains callable by migration clients without holding a log handle
// or starting background work.
//
// Deprecated: automatic log-triggered quality checks have been retired.
func (s *Service) WatchLog() {}

// LogLine accepts historical markers without changing state or issuing requests.
// Manual quality checks still use Trigger explicitly.
//
// Deprecated: automatic log-triggered quality checks have been retired.
func (s *Service) LogLine(line string) {}
