#!/usr/bin/env python3
"""Migrate data from SQLite to PostgreSQL.

Usage:
    python3 scripts/migrate_to_postgres.py --sqlite data/whatsapp.db --pg "postgres://bridge:PASSWORD@localhost:5432/whatsapp?sslmode=disable"

This script is idempotent - safe to run multiple times. Uses ON CONFLICT to skip duplicates.
The SQLite database is NOT modified.
"""

import argparse
import sqlite3
import sys

try:
    import psycopg2
except ImportError:
    print("Error: psycopg2 not installed. Run: pip install psycopg2-binary")
    sys.exit(1)


def migrate_table(sqlite_cur, pg_cur, table, columns, conflict_cols, bool_cols=None):
    """Migrate a single table from SQLite to PostgreSQL."""
    bool_cols = set(bool_cols or [])
    bool_indices = {i for i, c in enumerate(columns) if c in bool_cols}

    col_list = ", ".join(columns)
    placeholders = ", ".join(["%s"] * len(columns))
    conflict_list = ", ".join(conflict_cols)

    # Build ON CONFLICT clause
    if conflict_cols:
        update_cols = [c for c in columns if c not in conflict_cols]
        if update_cols:
            update_clause = ", ".join(f"{c} = EXCLUDED.{c}" for c in update_cols)
            on_conflict = f"ON CONFLICT ({conflict_list}) DO UPDATE SET {update_clause}"
        else:
            on_conflict = f"ON CONFLICT ({conflict_list}) DO NOTHING"
    else:
        on_conflict = ""

    insert_sql = f"INSERT INTO {table} ({col_list}) VALUES ({placeholders}) {on_conflict}"

    sqlite_cur.execute(f"SELECT {col_list} FROM {table}")
    rows = sqlite_cur.fetchall()

    if not rows:
        print(f"  {table}: 0 rows (empty)")
        return 0

    batch_size = 500
    total = 0
    for i in range(0, len(rows), batch_size):
        batch = rows[i:i + batch_size]
        for row in batch:
            converted = []
            for idx, val in enumerate(row):
                if val is None:
                    converted.append(None)
                elif idx in bool_indices:
                    converted.append(bool(val))
                else:
                    converted.append(val)
            pg_cur.execute(insert_sql, converted)
        total += len(batch)

    print(f"  {table}: {total} rows migrated")
    return total


def main():
    parser = argparse.ArgumentParser(description="Migrate WhatsApp Bridge data from SQLite to PostgreSQL")
    parser.add_argument("--sqlite", required=True, help="Path to SQLite database file")
    parser.add_argument("--pg", required=True, help="PostgreSQL connection string")
    args = parser.parse_args()

    print(f"Source: {args.sqlite}")
    print(f"Target: PostgreSQL")
    print()

    # Connect to SQLite (read-only)
    sqlite_conn = sqlite3.connect(f"file:{args.sqlite}?mode=ro", uri=True)
    sqlite_cur = sqlite_conn.cursor()

    # Connect to PostgreSQL
    pg_conn = psycopg2.connect(args.pg)
    pg_conn.autocommit = False
    pg_cur = pg_conn.cursor()

    try:
        print("Migrating tables...")

        migrate_table(sqlite_cur, pg_cur, "chats",
            ["jid", "name", "is_group", "unread_count", "last_message_time", "last_message_preview"],
            ["jid"],
            bool_cols=["is_group"])

        migrate_table(sqlite_cur, pg_cur, "contacts",
            ["jid", "name", "notify", "phone"],
            ["jid"])

        migrate_table(sqlite_cur, pg_cur, "messages",
            ["id", "chat_jid", "sender", "sender_name", "content", "timestamp", "is_from_me",
             "media_type", "mime_type", "filename", "media_key", "file_sha256", "file_enc_sha256",
             "file_length", "push_name", "quoted_message_id", "quoted_participant",
             "transcription", "media_url", "direct_path", "is_edited", "is_revoked", "edited_at"],
            ["id", "chat_jid"],
            bool_cols=["is_from_me", "is_edited", "is_revoked"])

        migrate_table(sqlite_cur, pg_cur, "watermarks",
            ["jid", "last_timestamp_ms"],
            ["jid"])

        migrate_table(sqlite_cur, pg_cur, "links",
            ["url", "platform", "title", "sender_jid", "chat_jid", "message_id", "timestamp"],
            [])  # No conflict - auto-increment ID

        migrate_table(sqlite_cur, pg_cur, "telemetry_daily",
            ["date", "messages_sent", "messages_received", "media_downloaded", "media_sent", "links_indexed"],
            ["date"])

        migrate_table(sqlite_cur, pg_cur, "telemetry_tool_calls",
            ["tool_name", "duration_ms", "success", "error_msg"],
            [],  # No conflict - auto-increment ID
            bool_cols=["success"])

        migrate_table(sqlite_cur, pg_cur, "reactions",
            ["message_id", "chat_jid", "reactor_jid", "reactor_name", "emoji", "timestamp"],
            ["message_id", "chat_jid", "reactor_jid"])

        pg_conn.commit()
        print()
        print("Migration complete. SQLite database was NOT modified.")

    except Exception as e:
        pg_conn.rollback()
        print(f"\nError: {e}")
        print("Migration rolled back. No changes were made to PostgreSQL.")
        sys.exit(1)
    finally:
        sqlite_cur.close()
        sqlite_conn.close()
        pg_cur.close()
        pg_conn.close()


if __name__ == "__main__":
    main()
