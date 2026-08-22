package server

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// idleTimeoutReader bounds the gap between successful reads while preserving
// an unlimited total stream duration. Closing the underlying response from
// the timeout path unblocks network reads and lets cancellation finish.
type idleTimeoutReader struct {
	r        io.ReadCloser
	timeout  time.Duration
	mu       sync.Mutex
	timedOut error
}

func withIdleTimeout(r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if timeout <= 0 {
		return r
	}
	return &idleTimeoutReader{r: r, timeout: timeout}
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.timedOut != nil {
		err := r.timedOut
		r.mu.Unlock()
		return 0, err
	}
	r.mu.Unlock()

	type result struct {
		n   int
		err error
	}
	readCh := make(chan result, 1)
	go func() {
		n, err := r.r.Read(p)
		readCh <- result{n: n, err: err}
	}()
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case result := <-readCh:
		return result.n, result.err
	case <-timer.C:
		err := fmt.Errorf("upstream stream idle timeout after %s", r.timeout)
		r.mu.Lock()
		r.timedOut = err
		r.mu.Unlock()
		_ = r.r.Close()
		return 0, err
	}
}

func (r *idleTimeoutReader) Close() error { return r.r.Close() }
