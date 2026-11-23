package control

import (
	"math/rand"
	"time"
)

// Backoff is exponential backoff with ±50% jitter, capped at max. Each call to
// Next returns the next wait and doubles the base for the call after.
type Backoff struct {
	base time.Duration
	max  time.Duration
	cur  time.Duration
	rnd  func() float64
}

// NewBackoff starts at base and never returns more than max.
func NewBackoff(base, max time.Duration) *Backoff {
	return &Backoff{base: base, max: max, cur: base, rnd: rand.Float64}
}

// Next returns the wait before the next attempt.
func (b *Backoff) Next() time.Duration {
	jitter := 0.5 + b.rnd() // uniform in [0.5, 1.5)
	d := time.Duration(float64(b.cur) * jitter)
	if d > b.max {
		d = b.max
	}
	if b.cur < b.max {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	return d
}

// Reset returns the backoff to its base, after a successful registration.
func (b *Backoff) Reset() {
	b.cur = b.base
}
