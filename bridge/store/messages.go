package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Message struct {
	ID                string `json:"id"`
	ChatJID           string `json:"chat_jid"`
	Sender            string `json:"sender"`
	SenderName        string `json:"sender_name"`
	Content           string `json:"content"`
	Timestamp         int64  `json:"timestamp"`
	IsFromMe          bool   `json:"is_from_me"`
	MediaType         string `json:"media_type,omitempty"`
	MimeType          string `json:"mime_type,omitempty"`
	Filename          string `json:"filename,omitempty"`
	MediaKey          []byte `json:"-"`
	FileSHA256        []byte `json:"-"`
	FileEncSHA256     []byte `json:"-"`
	FileLength        int64  `json:"file_length,omitempty"`
	MediaURL          string `json:"-"`
	DirectPath        string `json:"-"`
	PushName          string `json:"push_name,omitempty"`
	QuotedMessageID   string `json:"quoted_message_id,omitempty"`
	QuotedParticipant string `json:"quoted_participant,omitempty"`
}

func (db *DB) UpsertMessage(m *Message) {
	db.Exec(`
		INSERT INTO messages (id, chat_jid, sender, sender_name, content, timestamp, is_from_me,
			media_type, mime_type, filename, media_key, file_sha256, file_enc_sha256, file_length,
			media_url, direct_path, push_name, quoted_message_id, quoted_participant)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, chat_jid) DO UPDATE SET
			content=excluded.content, sender_name=excluded.sender_name,
			push_name=excluded.push_name, media_type=excluded.media_type,
			mime_type=excluded.mime_type, filename=excluded.filename,
			media_key=excluded.media_key, file_sha256=excluded.file_sha256,
			file_enc_sha256=excluded.file_enc_sha256, file_length=excluded.file_length,
			media_url=excluded.media_url, direct_path=excluded.direct_path`,
		m.ID, m.ChatJID, m.Sender, m.SenderName, m.Content, m.Timestamp, m.IsFromMe,
		m.MediaType, m.MimeType, m.Filename, m.MediaKey, m.FileSHA256, m.FileEncSHA256,
		m.FileLength, m.MediaURL, m.DirectPath, m.PushName, m.QuotedMessageID, m.QuotedParticipant)
}

type MessageQuery struct {
	ChatJID string
	Sender  string
	After   int64 // Unix ms
	Before  int64 // Unix ms
	Query   string
	Limit   int
	Offset  int
}

func (db *DB) QueryMessages(q MessageQuery) ([]Message, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}

	where := []string{"1=1"}
	args := []any{}

	if q.ChatJID != "" {
		where = append(where, "chat_jid = ?")
		args = append(args, q.ChatJID)
	}
	if q.Sender != "" {
		where = append(where, "sender = ?")
		args = append(args, q.Sender)
	}
	if q.After > 0 {
		where = append(where, "timestamp > ?")
		args = append(args, q.After)
	}
	if q.Before > 0 {
		where = append(where, "timestamp < ?")
		args = append(args, q.Before)
	}
	if q.Query != "" {
		where = append(where, "content LIKE ?")
		args = append(args, "%"+q.Query+"%")
	}

	whereClause := strings.Join(where, " AND ")

	// Count
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM messages WHERE %s", whereClause)
	if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count messages: %w", err)
	}

	// Fetch
	selectQuery := fmt.Sprintf(`SELECT id, chat_jid, sender, sender_name, content, timestamp,
		is_from_me, media_type, mime_type, filename, file_length, push_name,
		quoted_message_id, quoted_participant
		FROM messages WHERE %s ORDER BY timestamp DESC LIMIT ? OFFSET ?`, whereClause)

	args = append(args, q.Limit, q.Offset)
	rows, err := db.Query(selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows, total)
}

func (db *DB) GetMessageContext(id, chatJID string, context int) ([]Message, error) {
	if context <= 0 {
		context = 5
	}

	// Get the target message's timestamp
	var ts int64
	err := db.QueryRow("SELECT timestamp FROM messages WHERE id = ? AND chat_jid = ?", id, chatJID).Scan(&ts)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}

	// context is in minutes, timestamps are in milliseconds
	windowMs := int64(context) * 60 * 1000
	rows, err := db.Query(`
		SELECT id, chat_jid, sender, sender_name, content, timestamp,
			is_from_me, media_type, mime_type, filename, file_length, push_name,
			quoted_message_id, quoted_participant
		FROM messages WHERE chat_jid = ? AND timestamp BETWEEN ? AND ?
		ORDER BY timestamp ASC`,
		chatJID, ts-windowMs, ts+windowMs)
	if err != nil {
		return nil, fmt.Errorf("query context: %w", err)
	}
	defer rows.Close()

	msgs, _, err := scanMessages(rows, 0)
	return msgs, err
}

// CheckNewMessages returns messages newer than the stored watermark for a JID,
// then updates the watermark.
func (db *DB) CheckNewMessages(jid string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}

	// Get current watermark
	var watermark int64
	err := db.QueryRow("SELECT last_timestamp_ms FROM watermarks WHERE jid = ?", jid).Scan(&watermark)
	if err == sql.ErrNoRows {
		// First check — set watermark to now so we don't return entire history
		now := time.Now().UnixMilli()
		db.Exec("INSERT INTO watermarks (jid, last_timestamp_ms) VALUES (?, ?) ON CONFLICT(jid) DO UPDATE SET last_timestamp_ms = excluded.last_timestamp_ms", jid, now)
		watermark = now
	} else if err != nil {
		return nil, fmt.Errorf("get watermark: %w", err)
	}

	// Query messages after watermark
	rows, err := db.Query(`
		SELECT id, chat_jid, sender, sender_name, content, timestamp,
			is_from_me, media_type, mime_type, filename, file_length, push_name,
			quoted_message_id, quoted_participant
		FROM messages
		WHERE chat_jid = ? AND timestamp > ? AND is_from_me = FALSE
		ORDER BY timestamp ASC LIMIT ?`,
		jid, watermark, limit)
	if err != nil {
		return nil, fmt.Errorf("check new: %w", err)
	}
	defer rows.Close()

	msgs, _, err := scanMessages(rows, 0)
	if err != nil {
		return nil, err
	}

	// Update watermark to latest message timestamp
	if len(msgs) > 0 {
		latest := msgs[len(msgs)-1].Timestamp
		db.Exec("INSERT INTO watermarks (jid, last_timestamp_ms) VALUES (?, ?) ON CONFLICT(jid) DO UPDATE SET last_timestamp_ms = excluded.last_timestamp_ms", jid, latest)
	}

	return msgs, nil
}

// TriggerFilters mirrors api.TriggerFilters for the store layer.
type TriggerFilters struct {
	MentionJID string
	SenderJIDs []string
}

// TriggerGroupResult holds messages for a single JID.
type TriggerGroupResult struct {
	Count    int       `json:"count"`
	Messages []Message `json:"messages"`
}

// TriggerResponse is the result of CheckTriggersMulti.
type TriggerResponse struct {
	Total  int                           `json:"total"`
	Groups map[string]TriggerGroupResult `json:"groups"`
}

// CheckTriggersMulti checks multiple JIDs for new messages in a single call,
// using per-JID watermarks. This replaces N separate CheckNewMessages calls.
func (db *DB) CheckTriggersMulti(jids []string, filters TriggerFilters, limit int) (*TriggerResponse, error) {
	if limit <= 0 {
		limit = 100
	}
	if len(jids) == 0 {
		return &TriggerResponse{Groups: map[string]TriggerGroupResult{}}, nil
	}

	// 1. Batch read all watermarks
	watermarks := make(map[string]int64)
	placeholders := make([]string, len(jids))
	args := make([]any, len(jids))
	for i, jid := range jids {
		placeholders[i] = "?"
		args[i] = jid
	}
	phStr := strings.Join(placeholders, ", ")

	rows, err := db.Query(fmt.Sprintf("SELECT jid, last_timestamp_ms FROM watermarks WHERE jid IN (%s)", phStr), args...)
	if err != nil {
		return nil, fmt.Errorf("batch read watermarks: %w", err)
	}
	for rows.Next() {
		var jid string
		var ts int64
		if err := rows.Scan(&jid, &ts); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan watermark: %w", err)
		}
		watermarks[jid] = ts
	}
	rows.Close()

	// 2. Initialize missing watermarks to now
	now := time.Now().UnixMilli()
	for _, jid := range jids {
		if _, exists := watermarks[jid]; !exists {
			db.Exec("INSERT INTO watermarks (jid, last_timestamp_ms) VALUES (?, ?) ON CONFLICT(jid) DO NOTHING", jid, now)
			watermarks[jid] = now
		}
	}

	// 3. Query messages per-JID (each has its own watermark threshold)
	// Build a UNION query or iterate per JID. Given that JID count is small (5-10),
	// iterating is cleaner and avoids complex SQL.
	resp := &TriggerResponse{
		Groups: make(map[string]TriggerGroupResult),
	}
	totalCount := 0

	for _, jid := range jids {
		wm := watermarks[jid]

		queryStr := `SELECT id, chat_jid, sender, sender_name, content, timestamp,
			is_from_me, media_type, mime_type, filename, file_length, push_name,
			quoted_message_id, quoted_participant
			FROM messages
			WHERE chat_jid = ? AND timestamp > ? AND is_from_me = FALSE
			ORDER BY timestamp ASC LIMIT ?`

		msgRows, err := db.Query(queryStr, jid, wm, limit)
		if err != nil {
			return nil, fmt.Errorf("query messages for %s: %w", jid, err)
		}

		msgs, _, err := scanMessages(msgRows, 0)
		if err != nil {
			return nil, fmt.Errorf("scan messages for %s: %w", jid, err)
		}

		if len(msgs) == 0 {
			continue
		}

		// Update watermark to latest message timestamp for this JID
		latest := msgs[len(msgs)-1].Timestamp
		db.Exec("INSERT INTO watermarks (jid, last_timestamp_ms) VALUES (?, ?) ON CONFLICT(jid) DO UPDATE SET last_timestamp_ms = excluded.last_timestamp_ms", jid, latest)

		resp.Groups[jid] = TriggerGroupResult{
			Count:    len(msgs),
			Messages: msgs,
		}
		totalCount += len(msgs)
	}

	resp.Total = totalCount
	return resp, nil
}

// GetMessageByID retrieves a single message including media_key fields (for download).
func (db *DB) GetMessageByID(id, chatJID string) (*Message, error) {
	row := db.QueryRow(`
		SELECT id, chat_jid, sender, sender_name, content, timestamp,
			is_from_me, media_type, mime_type, filename, media_key, file_sha256,
			file_enc_sha256, file_length, media_url, direct_path,
			push_name, quoted_message_id, quoted_participant
		FROM messages WHERE id = ? AND chat_jid = ?`, id, chatJID)

	m := &Message{}
	err := row.Scan(&m.ID, &m.ChatJID, &m.Sender, &m.SenderName, &m.Content, &m.Timestamp,
		&m.IsFromMe, &m.MediaType, &m.MimeType, &m.Filename, &m.MediaKey, &m.FileSHA256,
		&m.FileEncSHA256, &m.FileLength, &m.MediaURL, &m.DirectPath,
		&m.PushName, &m.QuotedMessageID, &m.QuotedParticipant)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}
	return m, nil
}

// MarkRevoked marks a message as revoked (deleted for everyone).
func (db *DB) MarkRevoked(id, chatJID string) {
	db.Exec(
		"UPDATE messages SET is_revoked = 1 WHERE id = ? AND chat_jid = ?",
		id, chatJID)
}

// UpdateLocalPath stores the local file path for a downloaded media file.
func (db *DB) UpdateLocalPath(id, chatJID, localPath string) {
	db.Exec(
		"UPDATE messages SET local_path = ? WHERE id = ? AND chat_jid = ?",
		localPath, id, chatJID)
}

// UpdateEditedContent updates a message's content after an edit.
func (db *DB) UpdateEditedContent(id, chatJID, newContent string) {
	db.Exec(
		"UPDATE messages SET content = ?, is_edited = 1, edited_at = ? WHERE id = ? AND chat_jid = ?",
		newContent, time.Now().UnixMilli(), id, chatJID)
}

func scanMessages(rows *sql.Rows, total int) ([]Message, int, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.Sender, &m.SenderName, &m.Content, &m.Timestamp,
			&m.IsFromMe, &m.MediaType, &m.MimeType, &m.Filename, &m.FileLength, &m.PushName,
			&m.QuotedMessageID, &m.QuotedParticipant); err != nil {
			return nil, 0, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, total, rows.Err()
}
