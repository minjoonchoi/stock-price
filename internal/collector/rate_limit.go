package collector

import (
	"net/http"
	"sync"
	"time"
)

type RateLimitedTransport struct {
	Base  http.RoundTripper
	Delay time.Duration

	mu      sync.Mutex
	last    time.Time
	now     func() time.Time
	sleep   func(time.Duration)
	started bool
}

func (t *RateLimitedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.wait()
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(request)
}

func (t *RateLimitedTransport) wait() {
	if t.Delay <= 0 {
		return
	}
	nowFunc := t.now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	sleepFunc := t.sleep
	if sleepFunc == nil {
		sleepFunc = time.Sleep
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := nowFunc()
	if t.started {
		next := t.last.Add(t.Delay)
		if now.Before(next) {
			duration := next.Sub(now)
			sleepFunc(duration)
			now = now.Add(duration)
		}
	}
	t.last = now
	t.started = true
}
