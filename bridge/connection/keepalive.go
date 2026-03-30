package connection

import "sync/atomic"

// KeepaliveTracker counts consecutive keepalive failures.
// After MaxFailures consecutive timeouts, the caller should force a reconnect.
type KeepaliveTracker struct {
	failures    atomic.Int32
	MaxFailures int32
}

func NewKeepaliveTracker(maxFailures int32) *KeepaliveTracker {
	return &KeepaliveTracker{MaxFailures: maxFailures}
}

func (kt *KeepaliveTracker) RecordFailure() int32 {
	return kt.failures.Add(1)
}

func (kt *KeepaliveTracker) Reset() {
	kt.failures.Store(0)
}

func (kt *KeepaliveTracker) ShouldReconnect() bool {
	return kt.failures.Load() >= kt.MaxFailures
}
