package api

// StatusResponse is returned by GET /api/status.
type StatusResponse struct {
	State        string         `json:"state"`
	IsConnected  bool           `json:"is_connected"`
	Uptime       string         `json:"uptime"`
	MessageCount int            `json:"message_count"`
	ChatCount    int            `json:"chat_count"`
	StartedAt    string         `json:"started_at"`
	Identity     *IdentityInfo  `json:"identity,omitempty"`
}

type IdentityInfo struct {
	JID      string `json:"jid"`
	Phone    string `json:"phone"`
	PushName string `json:"push_name"`
}

// SendRequest is the body for POST /api/send.
type SendRequest struct {
	To                string   `json:"to"`
	Text              string   `json:"text"`
	QuotedMessageID   string   `json:"quotedMessageId,omitempty"`
	QuotedParticipant string   `json:"quotedParticipant,omitempty"`
	Mentions          []string `json:"mentions,omitempty"`
}

// SendResponse is returned by POST /api/send and POST /api/send/media.
type SendResponse struct {
	Success   bool   `json:"success"`
	MessageID string `json:"message_id"`
}

// DownloadRequest is the body for POST /api/download.
type DownloadRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
	OutputDir string `json:"output_dir,omitempty"`
}

// DownloadResponse is returned by POST /api/download.
type DownloadResponse struct {
	FilePath  string `json:"file_path"`
	MediaType string `json:"media_type"`
	FileSize  int64  `json:"file_size"`
}

// ListResponse is a generic paginated list response.
type ListResponse struct {
	Data   any `json:"data"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// CheckResponse is returned by GET /api/check.
type CheckResponse struct {
	Messages any    `json:"messages"`
	Count    int    `json:"count"`
	JID      string `json:"jid"`
}

// ReactRequest is the body for POST /api/send/reaction.
type ReactRequest struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`  // empty string = remove reaction
	Sender    string `json:"sender"` // sender of the target message (empty = own message)
}

// EditRequest is the body for POST /api/send/edit.
type EditRequest struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	NewText   string `json:"new_text"`
}

// RevokeRequest is the body for POST /api/send/revoke.
type RevokeRequest struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	Sender    string `json:"sender"` // empty = revoke own message; set for admin revoking others
}

// TriggerFilters controls optional filtering for the trigger check endpoint.
type TriggerFilters struct {
	MentionJID string   `json:"mention_jid,omitempty"`
	SenderJIDs []string `json:"sender_jids,omitempty"`
}

// TriggerRequest is the body for POST /api/check/triggers.
type TriggerRequest struct {
	JIDs    []string       `json:"jids"`
	Filters TriggerFilters `json:"filters"`
	Limit   int            `json:"limit,omitempty"`
	DryRun  bool           `json:"dry_run,omitempty"`
}

// TriggerGroupResult holds messages for a single JID in the trigger response.
type TriggerGroupResult struct {
	Count    int `json:"count"`
	Messages any `json:"messages"`
}

// TriggerResponse is returned by POST /api/check/triggers.
type TriggerResponse struct {
	Total  int                           `json:"total"`
	Groups map[string]TriggerGroupResult `json:"groups"`
}

// ToolCallRequest is the body for POST /api/telemetry/tool.
type ToolCallRequest struct {
	ToolName   string `json:"tool_name"`
	DurationMs int    `json:"duration_ms"`
	Success    bool   `json:"success"`
	ErrorMsg   string `json:"error_msg"`
}
