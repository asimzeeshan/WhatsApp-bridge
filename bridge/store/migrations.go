package store

// migrations is an ordered list of DDL statements, indexed by version number.
// Version 0 is unused — migration numbering starts at 1.
var migrations = []string{
	"", // v0 placeholder
	// v1: core tables
	`CREATE TABLE IF NOT EXISTS chats (
		jid                  TEXT PRIMARY KEY,
		name                 TEXT NOT NULL DEFAULT '',
		is_group             INTEGER NOT NULL DEFAULT 0,
		unread_count         INTEGER NOT NULL DEFAULT 0,
		last_message_time    TEXT NOT NULL DEFAULT '',
		last_message_preview TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS contacts (
		jid    TEXT PRIMARY KEY,
		name   TEXT NOT NULL DEFAULT '',
		notify TEXT NOT NULL DEFAULT '',
		phone  TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS messages (
		id                  TEXT NOT NULL,
		chat_jid            TEXT NOT NULL,
		sender              TEXT NOT NULL DEFAULT '',
		sender_name         TEXT NOT NULL DEFAULT '',
		content             TEXT NOT NULL DEFAULT '',
		timestamp           INTEGER NOT NULL DEFAULT 0,
		is_from_me          INTEGER NOT NULL DEFAULT 0,
		media_type          TEXT NOT NULL DEFAULT '',
		mime_type            TEXT NOT NULL DEFAULT '',
		filename            TEXT NOT NULL DEFAULT '',
		media_key           BLOB,
		file_sha256         BLOB,
		file_enc_sha256     BLOB,
		file_length         INTEGER NOT NULL DEFAULT 0,
		push_name           TEXT NOT NULL DEFAULT '',
		quoted_message_id   TEXT NOT NULL DEFAULT '',
		quoted_participant  TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (id, chat_jid)
	);
	CREATE INDEX IF NOT EXISTS idx_messages_chat_time ON messages(chat_jid, timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender);
	CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp DESC);`,

	// v2: watermarks
	`CREATE TABLE IF NOT EXISTS watermarks (
		jid                TEXT PRIMARY KEY,
		last_timestamp_ms  INTEGER NOT NULL DEFAULT 0
	);`,

	// v3: links index
	`CREATE TABLE IF NOT EXISTS links (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		url         TEXT NOT NULL,
		platform    TEXT NOT NULL DEFAULT 'other',
		title       TEXT NOT NULL DEFAULT '',
		sender_jid  TEXT NOT NULL,
		chat_jid    TEXT NOT NULL,
		message_id  TEXT NOT NULL,
		timestamp   INTEGER NOT NULL DEFAULT 0,
		created_at  TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_links_platform ON links(platform);
	CREATE INDEX IF NOT EXISTS idx_links_chat_time ON links(chat_jid, timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_links_url ON links(url);`,

	// v4: telemetry
	`CREATE TABLE IF NOT EXISTS telemetry_daily (
		date              TEXT PRIMARY KEY,
		messages_sent     INTEGER NOT NULL DEFAULT 0,
		messages_received INTEGER NOT NULL DEFAULT 0,
		media_downloaded  INTEGER NOT NULL DEFAULT 0,
		media_sent        INTEGER NOT NULL DEFAULT 0,
		links_indexed     INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS telemetry_tool_calls (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		tool_name   TEXT NOT NULL,
		duration_ms INTEGER NOT NULL,
		success     INTEGER NOT NULL,
		error_msg   TEXT NOT NULL DEFAULT '',
		called_at   TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_telemetry_tool ON telemetry_tool_calls(tool_name, called_at DESC);`,

	// v5: daily summaries
	`CREATE TABLE IF NOT EXISTS daily_summaries (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		date            TEXT NOT NULL,
		chat_jid        TEXT NOT NULL,
		message_count   INTEGER NOT NULL DEFAULT 0,
		active_members  INTEGER NOT NULL DEFAULT 0,
		top_topics      TEXT NOT NULL DEFAULT '[]',
		media_count     INTEGER NOT NULL DEFAULT 0,
		links_shared    INTEGER NOT NULL DEFAULT 0,
		summary_text    TEXT NOT NULL DEFAULT '',
		generated_at    TEXT,
		UNIQUE(date, chat_jid)
	);`,

	// v6: transcription column on messages
	`ALTER TABLE messages ADD COLUMN transcription TEXT NOT NULL DEFAULT '';`,

	// v7: reactions table
	`CREATE TABLE IF NOT EXISTS reactions (
		message_id   TEXT NOT NULL,
		chat_jid     TEXT NOT NULL,
		reactor_jid  TEXT NOT NULL,
		reactor_name TEXT NOT NULL DEFAULT '',
		emoji        TEXT NOT NULL DEFAULT '',
		timestamp    INTEGER NOT NULL DEFAULT 0,
		UNIQUE(message_id, chat_jid, reactor_jid)
	);
	CREATE INDEX IF NOT EXISTS idx_reactions_message ON reactions(message_id, chat_jid);
	CREATE INDEX IF NOT EXISTS idx_reactions_chat ON reactions(chat_jid, timestamp DESC);`,

	// v8: media download fields (URL + DirectPath needed for whatsmeow Download)
	`ALTER TABLE messages ADD COLUMN media_url TEXT NOT NULL DEFAULT '';
	ALTER TABLE messages ADD COLUMN direct_path TEXT NOT NULL DEFAULT '';`,

	// v9: message edit/revoke tracking
	`ALTER TABLE messages ADD COLUMN is_edited INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE messages ADD COLUMN is_revoked INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE messages ADD COLUMN edited_at INTEGER NOT NULL DEFAULT 0;`,

	// v10: local file path for downloaded media
	`ALTER TABLE messages ADD COLUMN local_path TEXT NOT NULL DEFAULT '';`,
}
