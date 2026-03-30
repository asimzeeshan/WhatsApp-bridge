package store

type DailySummary struct {
	ID            int64  `json:"id"`
	Date          string `json:"date"`
	ChatJID       string `json:"chat_jid"`
	MessageCount  int    `json:"message_count"`
	ActiveMembers int    `json:"active_members"`
	TopTopics     string `json:"top_topics"`
	MediaCount    int    `json:"media_count"`
	LinksShared   int    `json:"links_shared"`
	SummaryText   string `json:"summary_text"`
	GeneratedAt   string `json:"generated_at,omitempty"`
}

// GetDailySummary is a stub — actual LLM summarization is Phase 2.
func (db *DB) GetDailySummary(date, chatJID string) (*DailySummary, error) {
	return nil, nil
}
