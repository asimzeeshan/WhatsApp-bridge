package store

// Reaction represents a message reaction (emoji) from a user.
type Reaction struct {
	MessageID  string `json:"message_id"`  // message being reacted to
	ChatJID    string `json:"chat_jid"`    // chat where reaction occurred
	ReactorJID string `json:"reactor_jid"` // who reacted
	ReactorName string `json:"reactor_name"` // push name of reactor
	Emoji      string `json:"emoji"`       // reaction emoji (empty = removed)
	Timestamp  int64  `json:"timestamp"`   // when the reaction was sent
}

// UpsertReaction inserts or updates a reaction. Empty emoji means removal.
func (db *DB) UpsertReaction(r *Reaction) {
	if r.Emoji == "" {
		// Reaction removed
		db.Exec(
			"DELETE FROM reactions WHERE message_id = ? AND chat_jid = ? AND reactor_jid = ?",
			r.MessageID, r.ChatJID, r.ReactorJID,
		)
	} else {
		db.Exec(`
			INSERT INTO reactions (message_id, chat_jid, reactor_jid, reactor_name, emoji, timestamp)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(message_id, chat_jid, reactor_jid) DO UPDATE SET
				emoji = excluded.emoji,
				reactor_name = excluded.reactor_name,
				timestamp = excluded.timestamp`,
			r.MessageID, r.ChatJID, r.ReactorJID, r.ReactorName, r.Emoji, r.Timestamp,
		)
	}
}

// GetReactions returns all reactions for a specific message.
func (db *DB) GetReactions(messageID, chatJID string) ([]Reaction, error) {
	rows, err := db.Query(`
		SELECT message_id, chat_jid, reactor_jid, reactor_name, emoji, timestamp
		FROM reactions WHERE message_id = ? AND chat_jid = ?
		ORDER BY timestamp ASC`, messageID, chatJID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reactions []Reaction
	for rows.Next() {
		var r Reaction
		if err := rows.Scan(&r.MessageID, &r.ChatJID, &r.ReactorJID, &r.ReactorName, &r.Emoji, &r.Timestamp); err != nil {
			return nil, err
		}
		reactions = append(reactions, r)
	}
	return reactions, rows.Err()
}
