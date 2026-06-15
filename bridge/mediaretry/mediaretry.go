// Package mediaretry coordinates WhatsApp media re-upload requests between the
// async event handler (which receives the re-uploaded path) and the synchronous
// download handler (which waits for it).
//
// BAN SAFETY: a media-retry receipt is an unusual outbound signal, and WhatsApp's
// anti-abuse systems flag bursts of unusual signals as robotic. So retries are
// throttled hard and on-demand only: paced (min interval), capped per day, deduped
// per message, and single-flight. There is deliberately NO bulk/background retry.
package mediaretry

import (
	"sync"
	"time"
)

// Result is delivered by the event handler to a waiting download request.
type Result struct {
	DirectPath string
	Err        error
}

// Ban-safe limits. Conservative on purpose — this should look like a human
// occasionally tapping one old voice note, never a recovery sweep.
const (
	minInterval  = 15 * time.Second // min gap between any two retry receipts
	dayCap       = 40               // max retry receipts per rolling 24h
	dedupeWindow = 24 * time.Hour   // never retry the same message twice within this
	waitTimeout  = 25 * time.Second // how long a download request waits for re-upload
)

var (
	mu       sync.Mutex
	waiters  = map[string]chan Result{}
	lastSend time.Time
	dayStart time.Time
	dayCount int
	tried    = map[string]time.Time{}
	inFlight bool // single-flight: at most one retry receipt outstanding
)

// Allow reports whether a retry receipt may be sent for msgID right now, applying
// pacing / daily-cap / dedupe / single-flight. If it returns true it has ALREADY
// consumed the budget and reserved the single-flight slot; the caller MUST send the
// receipt and later call Done(). If false, the caller must NOT send anything.
func Allow(msgID string, now time.Time) bool {
	mu.Lock()
	defer mu.Unlock()
	if inFlight {
		return false
	}
	if dayStart.IsZero() || now.Sub(dayStart) > 24*time.Hour {
		dayStart, dayCount = now, 0
		pruneLocked(now) // opportunistic cleanup of the dedupe map
	}
	if dayCount >= dayCap {
		return false
	}
	if !lastSend.IsZero() && now.Sub(lastSend) < minInterval {
		return false
	}
	if t, ok := tried[msgID]; ok && now.Sub(t) < dedupeWindow {
		return false
	}
	lastSend = now
	dayCount++
	tried[msgID] = now
	inFlight = true
	return true
}

// Done releases the single-flight slot. Always call after Allow returned true.
func Done() {
	mu.Lock()
	inFlight = false
	mu.Unlock()
}

// Register creates a waiter channel for msgID. Caller must Cleanup when done.
func Register(msgID string) chan Result {
	ch := make(chan Result, 1)
	mu.Lock()
	waiters[msgID] = ch
	mu.Unlock()
	return ch
}

// Deliver hands a result to a waiting request (no-op if nothing is waiting).
func Deliver(msgID string, r Result) {
	mu.Lock()
	ch, ok := waiters[msgID]
	mu.Unlock()
	if ok {
		select {
		case ch <- r:
		default:
		}
	}
}

// Cleanup removes the waiter for msgID.
func Cleanup(msgID string) {
	mu.Lock()
	delete(waiters, msgID)
	mu.Unlock()
}

// WaitTimeout is how long the request side should wait for the re-upload event.
func WaitTimeout() time.Duration { return waitTimeout }

// pruneLocked drops dedupe entries older than the window. Caller holds mu.
func pruneLocked(now time.Time) {
	for k, t := range tried {
		if now.Sub(t) > dedupeWindow {
			delete(tried, k)
		}
	}
}
