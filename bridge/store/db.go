package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
)

// DB wraps database connections and provides a unified interface for SQLite and PostgreSQL.
type DB struct {
	Read   *sql.DB
	Write  *sql.DB
	Writer *Writer // Only used for SQLite; nil for PostgreSQL
	logger *slog.Logger
	driver string // "sqlite" or "pgx"
}

// IsPostgres returns true if the database driver is PostgreSQL.
func (db *DB) IsPostgres() bool {
	return db.driver == "pgx"
}

// Exec executes a write query. For SQLite, enqueues via Writer. For PostgreSQL, executes directly.
func (db *DB) Exec(query string, args ...any) {
	if db.IsPostgres() {
		query = rewritePlaceholders(query)
		_, err := db.Write.Exec(query, args...)
		if err != nil {
			db.logger.Warn("exec failed", "error", err, "query", truncateQuery(query))
		}
	} else {
		db.Writer.Enqueue(query, args...)
	}
}

// Query runs a read query, rewriting placeholders for PostgreSQL.
func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	if db.IsPostgres() {
		query = rewritePlaceholders(query)
	}
	return db.Read.Query(query, args...)
}

// QueryRow runs a read query returning a single row, rewriting placeholders for PostgreSQL.
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	if db.IsPostgres() {
		query = rewritePlaceholders(query)
	}
	return db.Read.QueryRow(query, args...)
}

// Open creates or opens a database (SQLite or PostgreSQL), runs migrations,
// and starts the single-writer goroutine for SQLite.
func Open(dataDir string, logger *slog.Logger) (*DB, error) {
	return OpenWithDriver("sqlite", "", dataDir, logger)
}

// OpenWithDriver creates or opens a database with the specified driver.
func OpenWithDriver(driver, dsn, dataDir string, logger *slog.Logger) (*DB, error) {
	switch driver {
	case "sqlite", "":
		return openSQLite(dataDir, logger)
	case "postgres":
		return openPostgres(dsn, logger)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}
}

func openSQLite(dataDir string, logger *slog.Logger) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "whatsapp.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON", dbPath)

	writeDB, err := sql.Open("sqlite3", dsn+"&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)

	readDB, err := sql.Open("sqlite3", dsn+"&mode=ro")
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(4)

	db := &DB{
		Read:   readDB,
		Write:  writeDB,
		logger: logger,
		driver: "sqlite3",
	}

	if err := db.migrateSQLite(); err != nil {
		readDB.Close()
		writeDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	db.Writer = NewWriter(writeDB, logger)
	go db.Writer.Run()

	return db, nil
}

func openPostgres(dsn string, logger *slog.Logger) (*DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("PostgreSQL DSN is required")
	}

	pgDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	pgDB.SetMaxOpenConns(10)
	pgDB.SetMaxIdleConns(5)

	// Verify connection
	if err := pgDB.Ping(); err != nil {
		pgDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	db := &DB{
		Read:   pgDB, // Same connection pool for read and write
		Write:  pgDB,
		Writer: nil, // No Writer goroutine for PostgreSQL
		logger: logger,
		driver: "pgx",
	}

	if err := db.migratePostgres(); err != nil {
		pgDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	logger.Info("PostgreSQL connected", "dsn", sanitizeDSN(dsn))
	return db, nil
}

func (db *DB) migrateSQLite() error {
	_, err := db.Write.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	err = db.Write.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current)
	if err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}

	for v := current + 1; v < len(migrations); v++ {
		ddl := migrations[v]
		if ddl == "" {
			continue
		}

		tx, err := db.Write.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for v%d: %w", v, err)
		}

		if _, err := tx.Exec(ddl); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply v%d: %w", v, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", v); err != nil {
			tx.Rollback()
			return fmt.Errorf("record v%d: %w", v, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v%d: %w", v, err)
		}

		db.logger.Info("applied migration", "version", v)
	}

	return nil
}

func (db *DB) migratePostgres() error {
	_, err := db.Write.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	if err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	err = db.Write.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current)
	if err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}

	for v := current + 1; v < len(pgMigrations); v++ {
		ddl := pgMigrations[v]
		if ddl == "" {
			continue
		}

		tx, err := db.Write.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for v%d: %w", v, err)
		}

		if _, err := tx.Exec(ddl); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply v%d: %w", v, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES ($1)", v); err != nil {
			tx.Rollback()
			return fmt.Errorf("record v%d: %w", v, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v%d: %w", v, err)
		}

		db.logger.Info("applied migration", "version", v)
	}

	return nil
}

// Close drains the writer and closes database connections.
func (db *DB) Close() error {
	if db.Writer != nil {
		db.Writer.Stop()
	}
	var errs []error
	if err := db.Write.Close(); err != nil {
		errs = append(errs, err)
	}
	// For PostgreSQL, Read and Write are the same pool - only close once
	if !db.IsPostgres() {
		if err := db.Read.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}

// rewritePlaceholders converts SQLite-style ? placeholders to PostgreSQL $1, $2, etc.
func rewritePlaceholders(query string) string {
	n := 0
	var result strings.Builder
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			result.WriteString(fmt.Sprintf("$%d", n))
		} else {
			result.WriteByte(query[i])
		}
	}
	if n == 0 {
		return query
	}
	return result.String()
}

// sanitizeDSN removes password from DSN for logging.
func sanitizeDSN(dsn string) string {
	if idx := strings.Index(dsn, "password="); idx >= 0 {
		end := strings.IndexByte(dsn[idx:], ' ')
		if end < 0 {
			return dsn[:idx] + "password=***"
		}
		return dsn[:idx] + "password=***" + dsn[idx+end:]
	}
	// URL format: postgres://user:pass@host/db
	if strings.Contains(dsn, "://") && strings.Contains(dsn, "@") {
		atIdx := strings.Index(dsn, "@")
		schemeEnd := strings.Index(dsn, "://") + 3
		colonIdx := strings.Index(dsn[schemeEnd:atIdx], ":")
		if colonIdx >= 0 {
			return dsn[:schemeEnd+colonIdx+1] + "***" + dsn[atIdx:]
		}
	}
	return dsn
}

func truncateQuery(q string) string {
	if len(q) > 80 {
		return q[:80] + "..."
	}
	return q
}
