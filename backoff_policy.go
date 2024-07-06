package econ

import (
	"math/rand"
	"time"
)

type backoffFunc func(retry int) (sleep time.Duration)

// newBackoffPolicy creates a new backoff policy that waits between mind and maxd
// and adds jitter of at most 20% to the wait time.
func newBackoffPolicy(mind, maxd time.Duration) backoffFunc {
	r := rand.New(rand.NewSource(time.Now().Unix()))

	factor := time.Second
	for _, scale := range []time.Duration{time.Hour, time.Minute, time.Second, time.Millisecond, time.Microsecond, time.Nanosecond} {
		d := mind.Truncate(scale)
		if d > 0 {
			factor = scale
			break
		}
	}

	return func(retry int) (sleep time.Duration) {
		wait := 2 << max(0, min(32, retry)) * factor
		jitter := time.Duration(r.Int63n(max(1, int64(wait)/5))) // max 20% jitter
		wait = mind + wait + jitter
		if wait > maxd {
			wait = maxd
		}
		return wait
	}
}
