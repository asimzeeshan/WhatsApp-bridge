package store

// pgMigrations contains PostgreSQL-specific DDL, indexed by version number.
// Mirrors the SQLite migrations but uses PostgreSQL syntax.
var pgMigrations = []string{
	"", // v0 placeholder

	// v1: core tables
	`CREATE TABLE IF NOT EXISTS chats (
		jid                  TEXT PRIMARY KEY,
		name                 TEXT NOT NULL DEFAULT '',
		is_group             BOOLEAN NOT NULL DEFAULT FALSE,
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
		timestamp           BIGINT NOT NULL DEFAULT 0,
		is_from_me          BOOLEAN NOT NULL DEFAULT FALSE,
		media_type          TEXT NOT NULL DEFAULT '',
		mime_type            TEXT NOT NULL DEFAULT '',
		filename            TEXT NOT NULL DEFAULT '',
		media_key           BYTEA,
		file_sha256         BYTEA,
		file_enc_sha256     BYTEA,
		file_length         BIGINT NOT NULL DEFAULT 0,
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
		last_timestamp_ms  BIGINT NOT NULL DEFAULT 0
	);`,

	// v3: links index
	`CREATE TABLE IF NOT EXISTS links (
		id          SERIAL PRIMARY KEY,
		url         TEXT NOT NULL,
		platform    TEXT NOT NULL DEFAULT 'other',
		title       TEXT NOT NULL DEFAULT '',
		sender_jid  TEXT NOT NULL,
		chat_jid    TEXT NOT NULL,
		message_id  TEXT NOT NULL,
		timestamp   BIGINT NOT NULL DEFAULT 0,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
		id          SERIAL PRIMARY KEY,
		tool_name   TEXT NOT NULL,
		duration_ms INTEGER NOT NULL,
		success     BOOLEAN NOT NULL,
		error_msg   TEXT NOT NULL DEFAULT '',
		called_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_telemetry_tool ON telemetry_tool_calls(tool_name, called_at DESC);`,

	// v5: daily summaries
	`CREATE TABLE IF NOT EXISTS daily_summaries (
		id              SERIAL PRIMARY KEY,
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
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS transcription TEXT NOT NULL DEFAULT '';`,

	// v7: reactions table
	`CREATE TABLE IF NOT EXISTS reactions (
		message_id   TEXT NOT NULL,
		chat_jid     TEXT NOT NULL,
		reactor_jid  TEXT NOT NULL,
		reactor_name TEXT NOT NULL DEFAULT '',
		emoji        TEXT NOT NULL DEFAULT '',
		timestamp    BIGINT NOT NULL DEFAULT 0,
		UNIQUE(message_id, chat_jid, reactor_jid)
	);
	CREATE INDEX IF NOT EXISTS idx_reactions_message ON reactions(message_id, chat_jid);
	CREATE INDEX IF NOT EXISTS idx_reactions_chat ON reactions(chat_jid, timestamp DESC);`,

	// v8: media download fields
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS media_url TEXT NOT NULL DEFAULT '';
	ALTER TABLE messages ADD COLUMN IF NOT EXISTS direct_path TEXT NOT NULL DEFAULT '';`,

	// v9: message edit/revoke tracking
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_edited BOOLEAN NOT NULL DEFAULT FALSE;
	ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_revoked BOOLEAN NOT NULL DEFAULT FALSE;
	ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at BIGINT NOT NULL DEFAULT 0;`,

	// v10: local file path for downloaded media
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS local_path TEXT NOT NULL DEFAULT '';`,

	// v11: messages_media - one normalized home for every media-related concern.
	// Folds in what used to live across messages columns + media_download_state sidecar:
	//   - identification (media_type, mime_type, filename, file_length)
	//   - decryption / re-download material (media_key, *_sha256, media_url, direct_path)
	//   - download lifecycle (local_path, downloaded_at, attempts, errors, permanent failure)
	//   - transcription lifecycle (transcription, transcribed_at)
	// NULL semantics throughout: NULL = never attempted; empty string / '[silence]' = attempted, empty result.
	`CREATE TABLE IF NOT EXISTS messages_media (
		message_id                  TEXT        NOT NULL,
		chat_jid                    TEXT        NOT NULL,

		media_type                  TEXT        NOT NULL CHECK (media_type IN ('image','video','audio','sticker','document')),
		mime_type                   TEXT,
		filename                    TEXT,
		file_length                 BIGINT,

		media_key                   BYTEA,
		file_sha256                 BYTEA,
		file_enc_sha256             BYTEA,
		media_url                   TEXT,
		direct_path                 TEXT,

		local_path                  TEXT,
		downloaded_at               TIMESTAMPTZ,
		download_attempts           INTEGER     NOT NULL DEFAULT 0,
		download_last_error         TEXT,
		download_last_attempt_at    TIMESTAMPTZ,
		download_permanently_failed BOOLEAN     NOT NULL DEFAULT FALSE,

		transcription               TEXT,
		transcribed_at              TIMESTAMPTZ,

		created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

		PRIMARY KEY (message_id, chat_jid),
		FOREIGN KEY (message_id, chat_jid) REFERENCES messages(id, chat_jid) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_mm_pending_download ON messages_media (created_at)
		WHERE local_path IS NULL
		  AND NOT download_permanently_failed
		  AND media_type IN ('image','video','sticker','document');
	CREATE INDEX IF NOT EXISTS idx_mm_pending_transcription ON messages_media (created_at)
		WHERE transcribed_at IS NULL
		  AND media_type = 'audio'
		  AND local_path IS NOT NULL;
	CREATE INDEX IF NOT EXISTS idx_mm_file_sha256 ON messages_media (file_sha256)
		WHERE file_sha256 IS NOT NULL;`,

	// v12: backfill messages_media from the existing messages columns + media_download_state.
	// Idempotent (ON CONFLICT DO NOTHING) so it can be re-run safely.
	// Empty strings / zero-length bytea -> NULL (semantic NULL = "never attempted/unknown").
	// For historical rows the created_at proxy is the original message timestamp.
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
		NULLIF(m.media_key, ''::bytea),
		NULLIF(m.file_sha256, ''::bytea),
		NULLIF(m.file_enc_sha256, ''::bytea),
		NULLIF(m.media_url, ''),
		NULLIF(m.direct_path, ''),
		NULLIF(m.local_path, ''),
		CASE WHEN m.local_path <> '' THEN to_timestamp(m.timestamp / 1000.0) END,
		COALESCE(s.attempts, 0),
		NULLIF(s.last_error, ''),
		s.last_attempt_at,
		COALESCE(s.attempts >= 8, FALSE),
		NULLIF(m.transcription, ''),
		CASE WHEN m.transcription <> '' THEN to_timestamp(m.timestamp / 1000.0) END,
		to_timestamp(m.timestamp / 1000.0)
	FROM messages m
	LEFT JOIN media_download_state s ON s.message_id = m.id AND s.chat_jid = m.chat_jid
	WHERE m.media_type <> ''
	ON CONFLICT (message_id, chat_jid) DO NOTHING;`,

	// v13: legacy media columns and sidecar are no longer written by any code path.
	// Drop them; the partial index on messages.media_type is dropped along with the
	// column it depends on. media_download_state was absorbed by messages_media in v11/v12.
	`DROP INDEX IF EXISTS idx_messages_missing_media;
	ALTER TABLE messages
		DROP COLUMN IF EXISTS media_type,
		DROP COLUMN IF EXISTS mime_type,
		DROP COLUMN IF EXISTS filename,
		DROP COLUMN IF EXISTS media_key,
		DROP COLUMN IF EXISTS file_sha256,
		DROP COLUMN IF EXISTS file_enc_sha256,
		DROP COLUMN IF EXISTS file_length,
		DROP COLUMN IF EXISTS media_url,
		DROP COLUMN IF EXISTS direct_path,
		DROP COLUMN IF EXISTS local_path,
		DROP COLUMN IF EXISTS transcription;
	DROP TABLE IF EXISTS media_download_state;`,

	// v14: polls and poll votes
	`CREATE TABLE IF NOT EXISTS polls (
		message_id     TEXT NOT NULL,
		chat_jid       TEXT NOT NULL,
		creator        TEXT NOT NULL DEFAULT '',
		question       TEXT NOT NULL DEFAULT '',
		options        JSONB NOT NULL DEFAULT '[]',
		max_selections INTEGER NOT NULL DEFAULT 1,
		enc_key        BYTEA,
		created_at     BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (message_id, chat_jid)
	);

	CREATE TABLE IF NOT EXISTS poll_votes (
		poll_message_id  TEXT NOT NULL,
		chat_jid         TEXT NOT NULL,
		voter            TEXT NOT NULL,
		voter_name       TEXT NOT NULL DEFAULT '',
		selected_options JSONB NOT NULL DEFAULT '[]',
		voted_at         BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (poll_message_id, chat_jid, voter)
	);
	CREATE INDEX IF NOT EXISTS idx_poll_votes_poll ON poll_votes (poll_message_id, chat_jid);`,
}
