package store

import (
	"fmt"
	"time"
)

type DailyTelemetry struct {
	Date             string `json:"date"`
	MessagesSent     int    `json:"messages_sent"`
	MessagesReceived int    `json:"messages_received"`
	MediaDownloaded  int    `json:"media_downloaded"`
	MediaSent        int    `json:"media_sent"`
	LinksIndexed     int    `json:"links_indexed"`
}

type ToolCall struct {
	ID         int64  `json:"id"`
	ToolName   string `json:"tool_name"`
	DurationMs int    `json:"duration_ms"`
	Success    bool   `json:"success"`
	ErrorMsg   string `json:"error_msg,omitempty"`
	CalledAt   string `json:"called_at"`
}

func today() string {
	return time.Now().Format("2006-01-02")
}

// allowedTelemetryFields is the whitelist of fields that can be incremented.
var allowedTelemetryFields = map[string]bool{
	"messages_sent":     true,
	"messages_received": true,
	"media_downloaded":  true,
	"media_sent":        true,
	"links_indexed":     true,
}

func (db *DB) IncrementTelemetry(field string) {
	if !allowedTelemetryFields[field] {
		db.logger.Warn("rejected unknown telemetry field", "field", field)
		return
	}
	date := today()
	// field is from a fixed whitelist above, safe to interpolate
	db.Exec(fmt.Sprintf(`
		INSERT INTO telemetry_daily (date, %s) VALUES (?, 1)
		ON CONFLICT(date) DO UPDATE SET %s = %s + 1`, field, field, field), date)
}

func (db *DB) GetDailyTelemetry(date string) (*DailyTelemetry, error) {
	if date == "" {
		date = today()
	}

	t := &DailyTelemetry{Date: date}
	err := db.QueryRow(`
		SELECT date, messages_sent, messages_received, media_downloaded, media_sent, links_indexed
		FROM telemetry_daily WHERE date = ?`, date).
		Scan(&t.Date, &t.MessagesSent, &t.MessagesReceived, &t.MediaDownloaded, &t.MediaSent, &t.LinksIndexed)
	if err != nil {
		// No data for this date is not an error
		return t, nil
	}
	return t, nil
}

func (db *DB) InsertToolCall(tc *ToolCall) {
	db.Exec(`
		INSERT INTO telemetry_tool_calls (tool_name, duration_ms, success, error_msg)
		VALUES (?, ?, ?, ?)`,
		tc.ToolName, tc.DurationMs, tc.Success, tc.ErrorMsg)
}

type ToolCallQuery struct {
	ToolName string
	Limit    int
	Offset   int
}

func (db *DB) QueryToolCalls(q ToolCallQuery) ([]ToolCall, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}

	where := "1=1"
	args := []any{}
	if q.ToolName != "" {
		where += " AND tool_name = ?"
		args = append(args, q.ToolName)
	}

	var total int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM telemetry_tool_calls WHERE %s", where), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tool calls: %w", err)
	}

	rows, err := db.Query(fmt.Sprintf(`
		SELECT id, tool_name, duration_ms, success, error_msg, called_at
		FROM telemetry_tool_calls WHERE %s ORDER BY called_at DESC LIMIT ? OFFSET ?`, where),
		append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query tool calls: %w", err)
	}
	defer rows.Close()

	var calls []ToolCall
	for rows.Next() {
		var tc ToolCall
		if err := rows.Scan(&tc.ID, &tc.ToolName, &tc.DurationMs, &tc.Success, &tc.ErrorMsg, &tc.CalledAt); err != nil {
			return nil, 0, fmt.Errorf("scan tool call: %w", err)
		}
		calls = append(calls, tc)
	}
	return calls, total, rows.Err()
}
