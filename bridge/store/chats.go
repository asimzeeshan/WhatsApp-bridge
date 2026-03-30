package store

import (
	"fmt"
)

type Chat struct {
	JID                string `json:"jid"`
	Name               string `json:"name"`
	IsGroup            bool   `json:"is_group"`
	UnreadCount        int    `json:"unread_count"`
	LastMessageTime    string `json:"last_message_time,omitempty"`
	LastMessagePreview string `json:"last_message_preview,omitempty"`
}

func (db *DB) UpsertChat(c *Chat) {
	db.Exec(`
		INSERT INTO chats (jid, name, is_group, unread_count, last_message_time, last_message_preview)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name=CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
			is_group=excluded.is_group,
			unread_count=excluded.unread_count,
			last_message_time=excluded.last_message_time,
			last_message_preview=excluded.last_message_preview`,
		c.JID, c.Name, c.IsGroup, c.UnreadCount, c.LastMessageTime, c.LastMessagePreview)
}

func (db *DB) UpdateChatLastMessage(jid string, timestamp int64, preview string) {
	db.Exec(`
		UPDATE chats SET last_message_time = ?, last_message_preview = ? WHERE jid = ?`,
		timestamp, preview, jid)
}

func (db *DB) IncrementUnread(jid string) {
	db.Exec(`UPDATE chats SET unread_count = unread_count + 1 WHERE jid = ?`, jid)
}

func (db *DB) ResetUnread(jid string) {
	db.Exec(`UPDATE chats SET unread_count = 0 WHERE jid = ?`, jid)
}

type ChatQuery struct {
	Query   string
	Limit   int
	Offset  int
	GroupsOnly bool
}

func (db *DB) QueryChats(q ChatQuery) ([]Chat, int, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}

	where := "1=1"
	args := []any{}
	if q.Query != "" {
		where += " AND (name LIKE ? OR jid LIKE ?)"
		args = append(args, "%"+q.Query+"%", "%"+q.Query+"%")
	}
	if q.GroupsOnly {
		where += " AND is_group = TRUE"
	}

	var total int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM chats WHERE %s", where), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count chats: %w", err)
	}

	rows, err := db.Query(fmt.Sprintf(`
		SELECT jid, name, is_group, unread_count, last_message_time, last_message_preview
		FROM chats WHERE %s ORDER BY last_message_time DESC LIMIT ? OFFSET ?`, where),
		append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query chats: %w", err)
	}
	defer rows.Close()

	var chats []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.JID, &c.Name, &c.IsGroup, &c.UnreadCount, &c.LastMessageTime, &c.LastMessagePreview); err != nil {
			return nil, 0, fmt.Errorf("scan chat: %w", err)
		}
		chats = append(chats, c)
	}
	return chats, total, rows.Err()
}

func (db *DB) GetChat(jid string) (*Chat, error) {
	var c Chat
	err := db.QueryRow(`
		SELECT jid, name, is_group, unread_count, last_message_time, last_message_preview
		FROM chats WHERE jid = ?`, jid).Scan(
		&c.JID, &c.Name, &c.IsGroup, &c.UnreadCount, &c.LastMessageTime, &c.LastMessagePreview)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

type UnreadChat struct {
	Chat     Chat      `json:"chat"`
	Messages []Message `json:"messages"`
}

func (db *DB) GetUnreadChats(msgLimit int) ([]UnreadChat, error) {
	if msgLimit <= 0 {
		msgLimit = 5
	}

	rows, err := db.Query(`
		SELECT jid, name, is_group, unread_count, last_message_time, last_message_preview
		FROM chats WHERE unread_count > 0 ORDER BY last_message_time DESC`)
	if err != nil {
		return nil, fmt.Errorf("query unread chats: %w", err)
	}
	defer rows.Close()

	var result []UnreadChat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.JID, &c.Name, &c.IsGroup, &c.UnreadCount, &c.LastMessageTime, &c.LastMessagePreview); err != nil {
			return nil, fmt.Errorf("scan unread chat: %w", err)
		}

		msgs, _, err := db.QueryMessages(MessageQuery{
			ChatJID: c.JID,
			Limit:   msgLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("get messages for %s: %w", c.JID, err)
		}

		result = append(result, UnreadChat{Chat: c, Messages: msgs})
	}
	return result, rows.Err()
}

// FlatUnreadMessage is used for the flat unread view.
type FlatUnreadMessage struct {
	ChatJID    string `json:"chatJid"`
	ChatName   string `json:"chatName"`
	IsGroup    bool   `json:"isGroup"`
	MessageID  string `json:"messageId"`
	Participant string `json:"participant"`
	SenderName string `json:"senderName"`
	Text       string `json:"text"`
	Timestamp  int64  `json:"timestamp"`
}

func (db *DB) GetFlatUnreadMessages() ([]FlatUnreadMessage, error) {
	rows, err := db.Query(`
		SELECT m.id, m.chat_jid, c.name, c.is_group, m.sender, m.sender_name, m.content, m.timestamp
		FROM messages m
		JOIN chats c ON c.jid = m.chat_jid
		WHERE c.unread_count > 0 AND m.is_from_me = FALSE
		ORDER BY m.timestamp DESC
		LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("flat unread: %w", err)
	}
	defer rows.Close()

	var msgs []FlatUnreadMessage
	for rows.Next() {
		var m FlatUnreadMessage
		if err := rows.Scan(&m.MessageID, &m.ChatJID, &m.ChatName, &m.IsGroup,
			&m.Participant, &m.SenderName, &m.Text, &m.Timestamp); err != nil {
			return nil, fmt.Errorf("scan flat unread: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
