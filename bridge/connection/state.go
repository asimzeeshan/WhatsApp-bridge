package connection

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type State int

const (
	Disconnected State = iota
	Connecting
	QRWaiting
	Connected
	PermanentlyDisconnected
)

func (s State) String() string {
	switch s {
	case Disconnected:
		return "DISCONNECTED"
	case Connecting:
		return "CONNECTING"
	case QRWaiting:
		return "QR_WAITING"
	case Connected:
		return "CONNECTED"
	case PermanentlyDisconnected:
		return "PERMANENTLY_DISCONNECTED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", s)
	}
}

// StateTracker provides thread-safe connection state tracking with generation
// counters to prevent stale reconnect goroutines from acting.
type StateTracker struct {
	mu         sync.RWMutex
	state      State
	generation atomic.Int64
}

func NewStateTracker() *StateTracker {
	return &StateTracker{state: Disconnected}
}

func (st *StateTracker) Get() State {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.state
}

func (st *StateTracker) Set(s State) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.state = s
}

func (st *StateTracker) Generation() int64 {
	return st.generation.Load()
}

func (st *StateTracker) NextGeneration() int64 {
	return st.generation.Add(1)
}

func (st *StateTracker) IsConnected() bool {
	return st.Get() == Connected
}
