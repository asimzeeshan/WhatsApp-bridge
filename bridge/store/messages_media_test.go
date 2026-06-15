package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// These are integration tests: they create a throwaway PostgreSQL database, run the
// real migrations, and exercise the messages_media upsert. They SKIP (not fail) when
// Postgres is unreachable, so `go test ./...` stays green in environments without a DB.

var dbCounter int64

func testPGBase() string {
	if v := os.Getenv("TEST_PG_DSN_BASE"); v != "" {
		return v
	}
	return "postgres://bridge:bridge@localhost:5432/"
}

func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	base := testPGBase()

	admin, err := sql.Open("pgx", base+"postgres")
	if err != nil {
		t.Skipf("pgx driver unavailable: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("Postgres not reachable at %spostgres: %v", base, err)
	}

	// Unique, lowercase, <=63-char db name per test invocation.
	safe := strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, t.Name()))
	if len(safe) > 30 {
		safe = safe[:30]
	}
	dbName := fmt.Sprintf("wa_test_%s_%d_%d", safe, os.Getpid(), atomic.AddInt64(&dbCounter, 1))

	admin.Exec("DROP DATABASE IF EXISTS " + dbName)
	if _, err := admin.Exec("CREATE DATABASE " + dbName); err != nil {
		admin.Close()
		t.Fatalf("create test db: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := OpenWithDriver("postgres", base+dbName, "", logger)
	if err != nil {
		admin.Exec("DROP DATABASE IF EXISTS " + dbName)
		admin.Close()
		t.Fatalf("open+migrate test db %q: %v", dbName, err)
	}

	cleanup := func() {
		db.Write.Close()
		admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()", dbName)
		admin.Exec("DROP DATABASE IF EXISTS " + dbName)
		admin.Close()
	}
	return db, cleanup
}

func fullMediaMessage(id, chat string) *Message {
	return &Message{
		ID:            id,
		ChatJID:       chat,
		Sender:        "9990001@s.whatsapp.net",
		Content:       "",
		Timestamp:     1700000000000,
		MediaType:     "image",
		MimeType:      "image/jpeg",
		Filename:      "photo.jpg",
		FileLength:    12345,
		MediaKey:      []byte("media-key-0123456789abcdef0123456789ab"),
		FileSHA256:    []byte("file-sha-256-aaaa"),
		FileEncSHA256: []byte("file-enc-sha-256-bbbb"),
		MediaURL:      "https://mmg.whatsapp.net/v/orig",
		DirectPath:    "/v/t62/orig.enc",
	}
}

// The C1 regression: a history-sync re-ingest arrives with the crypto/identity fields
// stripped (empty). The upsert must COALESCE — keep the existing keys, never NULL them —
// or the media becomes permanently undecryptable.
func TestMediaUpsertPreservesKeysOnReingest(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	orig := fullMediaMessage("MSG1", "123@g.us")
	db.UpsertMessage(orig)

	// Re-ingest the SAME message with empty media identity/crypto fields.
	db.UpsertMessage(&Message{
		ID: "MSG1", ChatJID: "123@g.us", MediaType: "image", Timestamp: 1700000000000,
	})

	var gotKey, gotSha, gotEnc []byte
	var gotDirect, gotURL, gotMime, gotFile sql.NullString
	var gotLen sql.NullInt64
	err := db.QueryRow(
		`SELECT media_key, file_sha256, file_enc_sha256, direct_path, media_url, mime_type, filename, file_length
		   FROM messages_media WHERE message_id=? AND chat_jid=?`, "MSG1", "123@g.us",
	).Scan(&gotKey, &gotSha, &gotEnc, &gotDirect, &gotURL, &gotMime, &gotFile, &gotLen)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !bytes.Equal(gotKey, orig.MediaKey) {
		t.Errorf("media_key not preserved: got %q want %q", gotKey, orig.MediaKey)
	}
	if !bytes.Equal(gotSha, orig.FileSHA256) {
		t.Error("file_sha256 not preserved")
	}
	if !bytes.Equal(gotEnc, orig.FileEncSHA256) {
		t.Error("file_enc_sha256 not preserved")
	}
	if gotDirect.String != orig.DirectPath {
		t.Errorf("direct_path not preserved: got %q", gotDirect.String)
	}
	if gotURL.String != orig.MediaURL {
		t.Errorf("media_url not preserved: got %q", gotURL.String)
	}
	if gotMime.String != orig.MimeType {
		t.Errorf("mime_type not preserved: got %q", gotMime.String)
	}
	if gotFile.String != orig.Filename {
		t.Errorf("filename not preserved: got %q", gotFile.String)
	}
	if gotLen.Int64 != orig.FileLength {
		t.Errorf("file_length not preserved: got %d", gotLen.Int64)
	}
}

// Worker-owned columns (local_path, transcription) are not in the upsert's column list,
// so a re-ingest must leave them untouched.
func TestMediaReingestPreservesWorkerFields(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.UpsertMessage(fullMediaMessage("MSG2", "123@g.us"))
	db.Exec(`UPDATE messages_media SET local_path=?, transcription=?, transcription_lang=?, ocr_text=?
	          WHERE message_id=? AND chat_jid=?`,
		"/media/audios/2026-01-01/x.ogg", "salam bhai", "ur", "a poster with text",
		"MSG2", "123@g.us")

	db.UpsertMessage(&Message{ID: "MSG2", ChatJID: "123@g.us", MediaType: "image", Timestamp: 1700000000000})

	var lp, tr, lang, ocr sql.NullString
	err := db.QueryRow(
		`SELECT local_path, transcription, transcription_lang, ocr_text
		   FROM messages_media WHERE message_id=? AND chat_jid=?`, "MSG2", "123@g.us",
	).Scan(&lp, &tr, &lang, &ocr)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if lp.String != "/media/audios/2026-01-01/x.ogg" {
		t.Errorf("local_path clobbered by re-ingest: %q", lp.String)
	}
	if tr.String != "salam bhai" || lang.String != "ur" {
		t.Errorf("transcription/lang clobbered: %q / %q", tr.String, lang.String)
	}
	if ocr.String != "a poster with text" {
		t.Errorf("ocr_text clobbered: %q", ocr.String)
	}
}

// media_type is the one column the upsert always overwrites (not coalesced), since a
// later, fuller ingest may correct it.
func TestMediaTypeIsOverwritten(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.UpsertMessage(&Message{ID: "MSG3", ChatJID: "123@g.us", MediaType: "document", Timestamp: 1700000000000})
	db.UpsertMessage(&Message{ID: "MSG3", ChatJID: "123@g.us", MediaType: "image", Timestamp: 1700000000000})

	var mt string
	if err := db.QueryRow(`SELECT media_type FROM messages_media WHERE message_id=?`, "MSG3").Scan(&mt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if mt != "image" {
		t.Errorf("media_type = %q, want image (always overwritten)", mt)
	}
}

func TestMigrationsReachV15(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var v int
	if err := db.QueryRow("SELECT COALESCE(MAX(version),0) FROM schema_version").Scan(&v); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if v < 15 {
		t.Fatalf("schema version = %d, want >= 15 (ocr/lang/updated_at migration)", v)
	}
}

// v15 added an updated_at column on messages_media with a trigger that bumps it on UPDATE.
func TestUpdatedAtTriggerBumps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.UpsertMessage(fullMediaMessage("MSG4", "123@g.us"))
	var before sql.NullTime
	if err := db.QueryRow(`SELECT updated_at FROM messages_media WHERE message_id=?`, "MSG4").Scan(&before); err != nil {
		t.Fatalf("query updated_at: %v", err)
	}

	db.Exec(`UPDATE messages_media SET ocr_text=? WHERE message_id=?`, "now described", "MSG4")
	var after sql.NullTime
	if err := db.QueryRow(`SELECT updated_at FROM messages_media WHERE message_id=?`, "MSG4").Scan(&after); err != nil {
		t.Fatalf("query updated_at after: %v", err)
	}
	if before.Valid && after.Valid && !after.Time.After(before.Time) {
		t.Errorf("updated_at did not advance on UPDATE: before=%v after=%v", before.Time, after.Time)
	}
}
