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

	// v11: messages_media - normalized home for every media-related concern.
	// Mirrors the PostgreSQL version (see migrations_pg.go); SQLite uses BLOB instead
	// of BYTEA and TEXT instead of TIMESTAMPTZ. Same logical shape and constraints.
	`CREATE TABLE IF NOT EXISTS messages_media (
		message_id                  TEXT        NOT NULL,
		chat_jid                    TEXT        NOT NULL,

		media_type                  TEXT        NOT NULL CHECK (media_type IN ('image','video','audio','sticker','document')),
		mime_type                   TEXT,
		filename                    TEXT,
		file_length                 INTEGER,

		media_key                   BLOB,
		file_sha256                 BLOB,
		file_enc_sha256             BLOB,
		media_url                   TEXT,
		direct_path                 TEXT,

		local_path                  TEXT,
		downloaded_at               TEXT,
		download_attempts           INTEGER     NOT NULL DEFAULT 0,
		download_last_error         TEXT,
		download_last_attempt_at    TEXT,
		download_permanently_failed INTEGER     NOT NULL DEFAULT 0,

		transcription               TEXT,
		transcribed_at              TEXT,

		created_at                  TEXT        NOT NULL DEFAULT (datetime('now')),

		PRIMARY KEY (message_id, chat_jid),
		FOREIGN KEY (message_id, chat_jid) REFERENCES messages(id, chat_jid) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_mm_pending_download ON messages_media (created_at)
		WHERE local_path IS NULL
		  AND download_permanently_failed = 0
		  AND media_type IN ('image','video','sticker','document');
	CREATE INDEX IF NOT EXISTS idx_mm_pending_transcription ON messages_media (created_at)
		WHERE transcribed_at IS NULL
		  AND media_type = 'audio'
		  AND local_path IS NOT NULL;
	CREATE INDEX IF NOT EXISTS idx_mm_file_sha256 ON messages_media (file_sha256)
		WHERE file_sha256 IS NOT NULL;`,

	// v12: backfill messages_media from messages columns + media_download_state.
	// Idempotent. NULLIF unwraps empty-string sentinels into semantic NULL.
	// media_download_state is a downstream sidecar that may not exist in pure-OSS deployments;
	// LEFT JOIN tolerates that.
	`INSERT INTO messages_media (
		message_id, chat_jid, media_type, mime_type, filename, file_length,
		media_key, file_sha256, file_enc_sha256, media_url, direct_path,
		local_path, downloaded_at,
		download_attempts, download_last_error, download_last_attempt_at, download_permanently_failed,
		transcription, transcribed_at,
		created_at
	)
	SELECT
		m.id, m.chat_jid, m.media_type,
		NULLIF(m.mime_type, ''),
		NULLIF(m.filename, ''),
		NULLIF(m.file_length, 0),
		m.media_key, m.file_sha256, m.file_enc_sha256,
		NULLIF(m.media_url, ''),
		NULLIF(m.direct_path, ''),
		NULLIF(m.local_path, ''),
		CASE WHEN m.local_path <> '' THEN datetime(m.timestamp / 1000, 'unixepoch') END,
		0, NULL, NULL, 0,
		NULLIF(m.transcription, ''),
		CASE WHEN m.transcription <> '' THEN datetime(m.timestamp / 1000, 'unixepoch') END,
		datetime(m.timestamp / 1000, 'unixepoch')
	FROM messages m
	WHERE m.media_type <> ''
	ON CONFLICT (message_id, chat_jid) DO NOTHING;`,

	// v13: drop legacy media columns from messages and the absorbed sidecar.
	// SQLite supports ALTER TABLE DROP COLUMN since 3.35.0 (2021); pre-existing
	// SQLite deployments may not have media_download_state at all (LEFT JOIN in v12
	// tolerates that).
	`DROP INDEX IF EXISTS idx_messages_missing_media;
	ALTER TABLE messages DROP COLUMN media_type;
	ALTER TABLE messages DROP COLUMN mime_type;
	ALTER TABLE messages DROP COLUMN filename;
	ALTER TABLE messages DROP COLUMN media_key;
	ALTER TABLE messages DROP COLUMN file_sha256;
	ALTER TABLE messages DROP COLUMN file_enc_sha256;
	ALTER TABLE messages DROP COLUMN file_length;
	ALTER TABLE messages DROP COLUMN media_url;
	ALTER TABLE messages DROP COLUMN direct_path;
	ALTER TABLE messages DROP COLUMN local_path;
	ALTER TABLE messages DROP COLUMN transcription;
	DROP TABLE IF EXISTS media_download_state;`,

	// v14: polls and poll votes
	`CREATE TABLE IF NOT EXISTS polls (
		message_id     TEXT NOT NULL,
		chat_jid       TEXT NOT NULL,
		creator        TEXT NOT NULL DEFAULT '',
		question       TEXT NOT NULL DEFAULT '',
		options        TEXT NOT NULL DEFAULT '[]',
		max_selections INTEGER NOT NULL DEFAULT 1,
		enc_key        BLOB,
		created_at     INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (message_id, chat_jid)
	);

	CREATE TABLE IF NOT EXISTS poll_votes (
		poll_message_id  TEXT NOT NULL,
		chat_jid         TEXT NOT NULL,
		voter            TEXT NOT NULL,
		voter_name       TEXT NOT NULL DEFAULT '',
		selected_options TEXT NOT NULL DEFAULT '[]',
		voted_at         INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (poll_message_id, chat_jid, voter)
	);
	CREATE INDEX IF NOT EXISTS idx_poll_votes_poll ON poll_votes (poll_message_id, chat_jid);`,

	// v15: transcription_lang / OCR cache / auto-stamped updated_at (mirrors the PG
	// migration). SQLite uses an AFTER UPDATE trigger; recursive_triggers is off by
	// default so the self-UPDATE does not re-fire.
	`ALTER TABLE messages_media ADD COLUMN transcription_lang TEXT;
	ALTER TABLE messages_media ADD COLUMN ocr_text TEXT;
	ALTER TABLE messages_media ADD COLUMN ocred_at TIMESTAMP;
	ALTER TABLE messages_media ADD COLUMN updated_at TIMESTAMP;
	CREATE TRIGGER IF NOT EXISTS trg_messages_media_updated_at
		AFTER UPDATE ON messages_media FOR EACH ROW
		BEGIN
			UPDATE messages_media SET updated_at = CURRENT_TIMESTAMP
			WHERE message_id = NEW.message_id AND chat_jid = NEW.chat_jid;
		END;`,
}
