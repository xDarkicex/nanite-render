package render

import "time"

// timeT is an alias for time.Time so tests can inject a fake clock
// without importing time at the call site.
type timeT = time.Time

func timeNowReal() timeT {
	return time.Now()
}
