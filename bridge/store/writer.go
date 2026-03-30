package store

import (
	"database/sql"
	"log/slog"
)

const writerChanCap = 1000

// WriteOp represents a database write operation.
type WriteOp struct {
	Query string
	Args  []any
}

// Writer serializes all write operations through a single goroutine
// to avoid SQLite SQLITE_BUSY contention.
type Writer struct {
	db     *sql.DB
	ch     chan WriteOp
	done   chan struct{}
	logger *slog.Logger
}

func NewWriter(db *sql.DB, logger *slog.Logger) *Writer {
	return &Writer{
		db:     db,
		ch:     make(chan WriteOp, writerChanCap),
		done:   make(chan struct{}),
		logger: logger,
	}
}

// Enqueue sends a write operation to the writer goroutine.
// Returns false if the channel is full (operation dropped).
func (w *Writer) Enqueue(query string, args ...any) bool {
	select {
	case w.ch <- WriteOp{Query: query, Args: args}:
		return true
	default:
		w.logger.Warn("write channel full, dropping operation", "query", query[:min(80, len(query))])
		return false
	}
}

// Run processes write operations sequentially. Call from a goroutine.
func (w *Writer) Run() {
	defer close(w.done)
	for op := range w.ch {
		_, err := w.db.Exec(op.Query, op.Args...)
		if err != nil {
			w.logger.Error("write failed", "error", err, "query", op.Query[:min(80, len(op.Query))])
		}
	}
}

// Stop closes the write channel and waits for all pending operations to complete.
func (w *Writer) Stop() {
	close(w.ch)
	<-w.done
}
