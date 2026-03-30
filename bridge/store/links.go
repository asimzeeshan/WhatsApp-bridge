package store

import "fmt"

type Link struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Platform  string `json:"platform"`
	Title     string `json:"title"`
	SenderJID string `json:"sender_jid"`
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	Timestamp int64  `json:"timestamp"`
	CreatedAt string `json:"created_at,omitempty"`
}

func (db *DB) InsertLink(l *Link) {
	db.Exec(`
		INSERT INTO links (url, platform, title, sender_jid, chat_jid, message_id, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.URL, l.Platform, l.Title, l.SenderJID, l.ChatJID, l.MessageID, l.Timestamp)
}

func (db *DB) UpdateLinkTitle(id int64, title string) {
	db.Exec("UPDATE links SET title = ? WHERE id = ?", title, id)
}

type LinkQuery struct {
	ChatJID  string
	Platform string
	After    int64
	Before   int64
	Limit    int
	Offset   int
}

func (db *DB) QueryLinks(q LinkQuery) ([]Link, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}

	where := "1=1"
	args := []any{}
	if q.ChatJID != "" {
		where += " AND chat_jid = ?"
		args = append(args, q.ChatJID)
	}
	if q.Platform != "" {
		where += " AND platform = ?"
		args = append(args, q.Platform)
	}
	if q.After > 0 {
		where += " AND timestamp > ?"
		args = append(args, q.After)
	}
	if q.Before > 0 {
		where += " AND timestamp < ?"
		args = append(args, q.Before)
	}

	var total int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM links WHERE %s", where), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count links: %w", err)
	}

	rows, err := db.Query(fmt.Sprintf(`
		SELECT id, url, platform, title, sender_jid, chat_jid, message_id, timestamp, created_at
		FROM links WHERE %s ORDER BY timestamp DESC LIMIT ? OFFSET ?`, where),
		append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query links: %w", err)
	}
	defer rows.Close()

	var links []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.URL, &l.Platform, &l.Title, &l.SenderJID,
			&l.ChatJID, &l.MessageID, &l.Timestamp, &l.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, l)
	}
	return links, total, rows.Err()
}
