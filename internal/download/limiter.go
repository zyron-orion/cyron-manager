package download

import "sync"

type limiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	limit  int
	active int
	closed bool
}

func newLimiter(limit int) *limiter {
	if limit < 1 {
		limit = 1
	}
	l := &limiter{limit: limit}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *limiter) acquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.active >= l.limit && !l.closed {
		l.cond.Wait()
	}
	if l.closed {
		return false
	}
	l.active++
	return true
}

func (l *limiter) release() {
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	l.cond.Broadcast()
	l.mu.Unlock()
}

func (l *limiter) setLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	l.mu.Lock()
	l.limit = limit
	l.cond.Broadcast()
	l.mu.Unlock()
}

func (l *limiter) close() {
	l.mu.Lock()
	l.closed = true
	l.cond.Broadcast()
	l.mu.Unlock()
}
