package econ

import "time"

func newTimer(duration time.Duration) (t *time.Timer, drained bool) {
	return time.NewTimer(duration), false
}

// closeTimer should be used as a deferred function
// in order to cleanly shut down a timer
func closeTimer(timer *time.Timer, drained *bool) {
	if drained == nil {
		panic("drained bool pointer is nil")
	}
	if !timer.Stop() {
		if *drained {
			return
		}
		<-timer.C
		*drained = true
	}
}

// resetTimer sets drained to false after resetting the timer.
func resetTimer(timer *time.Timer, duration time.Duration, drained *bool) {
	if drained == nil {
		panic("drained bool pointer is nil")
	}
	if !timer.Stop() {
		if !*drained {
			<-timer.C
		}
	}
	timer.Reset(duration)
	*drained = false
}
