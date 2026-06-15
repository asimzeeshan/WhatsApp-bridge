package mediaretry

import (
	"testing"
	"time"
)

// resetState zeroes the package-level budget/waiter state so each test starts clean.
// White-box test (same package) so it can touch the unexported globals.
func resetState() {
	mu.Lock()
	defer mu.Unlock()
	waiters = map[string]chan Result{}
	lastSend = time.Time{}
	dayStart = time.Time{}
	dayCount = 0
	tried = map[string]time.Time{}
	inFlight = false
}

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestAllowFirstCallConsumesBudgetAndReservesSlot(t *testing.T) {
	resetState()
	if !Allow("m1", base) {
		t.Fatal("first Allow should succeed")
	}
	if dayCount != 1 {
		t.Fatalf("dayCount = %d, want 1", dayCount)
	}
	if !inFlight {
		t.Fatal("Allow(true) must reserve the single-flight slot")
	}
	if _, ok := tried["m1"]; !ok {
		t.Fatal("Allow must record the message in the dedupe map")
	}
}

func TestSingleFlightBlocksConcurrentRetry(t *testing.T) {
	resetState()
	if !Allow("m1", base) {
		t.Fatal("first Allow should succeed")
	}
	// Even a different message, well past the pacing interval, is blocked while one is in flight.
	if Allow("m2", base.Add(time.Hour)) {
		t.Fatal("single-flight must block a second Allow while one is outstanding")
	}
	Done() // release the slot
	if !Allow("m2", base.Add(time.Hour)) {
		t.Fatal("after Done, a new Allow should succeed")
	}
}

func TestPacingEnforcesMinInterval(t *testing.T) {
	resetState()
	Allow("m1", base)
	Done()
	if Allow("m2", base.Add(minInterval-time.Second)) {
		t.Fatal("Allow within minInterval must be blocked by pacing")
	}
	if !Allow("m2", base.Add(minInterval)) {
		t.Fatal("Allow at exactly minInterval should be permitted")
	}
}

func TestDedupeBlocksSameMessageWithinWindow(t *testing.T) {
	resetState()
	Allow("m1", base)
	Done()
	// Same message, past pacing but inside the dedupe window -> blocked.
	if Allow("m1", base.Add(minInterval+time.Second)) {
		t.Fatal("same message inside dedupe window must be blocked")
	}
	// A different message at the same time is fine.
	if !Allow("m2", base.Add(minInterval+time.Second)) {
		t.Fatal("different message should not be deduped")
	}
}

func TestDayCapBlocksOverLimit(t *testing.T) {
	resetState()
	for i := 0; i < dayCap; i++ {
		now := base.Add(time.Duration(i) * minInterval)
		if !Allow(msgID(i), now) {
			t.Fatalf("Allow #%d should succeed under the cap", i)
		}
		Done()
	}
	// The (dayCap+1)-th within the same rolling day must be blocked.
	over := base.Add(time.Duration(dayCap) * minInterval)
	if Allow("over", over) {
		t.Fatal("Allow beyond the daily cap must be blocked")
	}
}

func TestDayRolloverResetsCount(t *testing.T) {
	resetState()
	Allow("m1", base)
	Done()
	later := base.Add(25 * time.Hour) // past the 24h window
	if !Allow("m2", later) {
		t.Fatal("after the day rolls over, Allow should succeed")
	}
	if dayCount != 1 {
		t.Fatalf("dayCount = %d after rollover, want 1", dayCount)
	}
}

func TestAbortRollsBackBudgetButNotSlot(t *testing.T) {
	resetState()
	Allow("m1", base)
	Abort("m1")
	if dayCount != 0 {
		t.Fatalf("Abort must decrement dayCount; got %d, want 0", dayCount)
	}
	if _, ok := tried["m1"]; ok {
		t.Fatal("Abort must clear the dedupe entry so the message can be retried")
	}
	if !inFlight {
		t.Fatal("Abort must NOT release the single-flight slot (Done does that)")
	}
	Done()
	// Budget restored + dedupe cleared -> the same message is retryable once pacing allows.
	if !Allow("m1", base.Add(minInterval)) {
		t.Fatal("after Abort+Done the message should be retryable")
	}
}

func TestAbortNeverDrivesCountNegative(t *testing.T) {
	resetState()
	Abort("never-allowed") // dayCount already 0
	if dayCount != 0 {
		t.Fatalf("Abort must not drive dayCount negative; got %d", dayCount)
	}
}

func TestRegisterReturnsExistingChannel(t *testing.T) {
	resetState()
	ch1 := Register("m1")
	ch2 := Register("m1")
	if ch1 != ch2 {
		t.Fatal("Register must return the existing channel for the same message")
	}
}

func TestDeliverReachesWaiter(t *testing.T) {
	resetState()
	ch := Register("m1")
	Deliver("m1", Result{DirectPath: "/fresh/path"})
	select {
	case r := <-ch:
		if r.DirectPath != "/fresh/path" {
			t.Fatalf("got %q, want /fresh/path", r.DirectPath)
		}
	default:
		t.Fatal("Deliver should have placed a result on the waiter channel")
	}
}

func TestDeliverWithoutWaiterIsNoop(t *testing.T) {
	resetState()
	// Must not panic or block.
	Deliver("ghost", Result{DirectPath: "/x"})
}

func TestCleanupRemovesWaiter(t *testing.T) {
	resetState()
	Register("m1")
	Cleanup("m1")
	// After cleanup there is no waiter, so a Deliver is a silent no-op (and Register would make a fresh one).
	Deliver("m1", Result{DirectPath: "/x"})
	ch := Register("m1")
	select {
	case <-ch:
		t.Fatal("a post-cleanup channel must be empty (old delivery should not be buffered on it)")
	default:
	}
}

func TestDeliverIsNonBlockingWhenBufferFull(t *testing.T) {
	resetState()
	ch := Register("m1")
	Deliver("m1", Result{DirectPath: "/first"})
	// Second delivery must not block even though the buffer (cap 1) is full.
	Deliver("m1", Result{DirectPath: "/second"})
	r := <-ch
	if r.DirectPath != "/first" {
		t.Fatalf("first delivery should win the single buffer slot; got %q", r.DirectPath)
	}
}

func msgID(i int) string {
	return "m-" + time.Duration(i).String() + "-" + string(rune('a'+i%26))
}
