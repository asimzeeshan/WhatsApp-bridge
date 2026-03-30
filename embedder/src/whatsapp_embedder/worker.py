"""Embedding worker: reads messages from PostgreSQL, embeds them, stores vectors in pgvector.

Usage:
    whatsapp-embedder --pg "postgres://bridge:PASSWORD@localhost:5432/whatsapp?sslmode=disable"

Environment variables (alternative to CLI args):
    PG_DSN - PostgreSQL connection string
    EMBEDDING_MODEL - sentence-transformers model name (default: paraphrase-multilingual-MiniLM-L12-v2)
    BATCH_SIZE - messages per batch (default: 100)
    POLL_INTERVAL - seconds between polls (default: 30)
"""

import argparse
import logging
import os
import signal
import sys
import time

import psycopg2
from sentence_transformers import SentenceTransformer

logger = logging.getLogger(__name__)

DEFAULT_MODEL = "paraphrase-multilingual-MiniLM-L12-v2"
DEFAULT_BATCH_SIZE = 100
DEFAULT_POLL_INTERVAL = 30

# Graceful shutdown
running = True


def handle_signal(signum, frame):
    global running
    logger.info("received signal %s, shutting down...", signum)
    running = False


def ensure_pgvector(pg_conn):
    """Ensure pgvector extension and message_embeddings table exist."""
    with pg_conn.cursor() as cur:
        cur.execute("CREATE EXTENSION IF NOT EXISTS vector")

        cur.execute("""
            CREATE TABLE IF NOT EXISTS message_embeddings (
                id BIGSERIAL PRIMARY KEY,
                message_id TEXT NOT NULL,
                chat_jid TEXT NOT NULL,
                sender TEXT DEFAULT '',
                sender_name TEXT DEFAULT '',
                content TEXT DEFAULT '',
                timestamp BIGINT NOT NULL,
                is_from_me BOOLEAN DEFAULT FALSE,
                embedding vector(384) NOT NULL,
                created_at TIMESTAMPTZ DEFAULT NOW(),
                UNIQUE(message_id, chat_jid)
            )
        """)

        cur.execute("""
            CREATE INDEX IF NOT EXISTS idx_embeddings_hnsw ON message_embeddings
                USING hnsw (embedding vector_cosine_ops)
                WITH (m = 16, ef_construction = 100)
        """)
        cur.execute("CREATE INDEX IF NOT EXISTS idx_embeddings_chat ON message_embeddings(chat_jid)")
        cur.execute("CREATE INDEX IF NOT EXISTS idx_embeddings_sender ON message_embeddings(sender)")
        cur.execute("CREATE INDEX IF NOT EXISTS idx_embeddings_timestamp ON message_embeddings(timestamp DESC)")

    pg_conn.commit()
    logger.info("pgvector extension and message_embeddings table ready")


def ensure_embedded_at_column(pg_conn):
    """Add embedded_at column to messages table if it doesn't exist."""
    with pg_conn.cursor() as cur:
        cur.execute("""
            SELECT column_name FROM information_schema.columns
            WHERE table_name = 'messages' AND column_name = 'embedded_at'
        """)
        if cur.fetchone() is None:
            cur.execute("ALTER TABLE messages ADD COLUMN embedded_at TIMESTAMPTZ")
            cur.execute("""
                CREATE INDEX IF NOT EXISTS idx_messages_not_embedded
                ON messages(timestamp) WHERE embedded_at IS NULL
            """)
            pg_conn.commit()
            logger.info("added embedded_at column and index to messages table")


def fetch_unembedded(pg_conn, batch_size: int) -> list[dict]:
    """Fetch messages that haven't been embedded yet."""
    with pg_conn.cursor() as cur:
        cur.execute("""
            SELECT id, chat_jid, sender, sender_name, content, timestamp, is_from_me
            FROM messages
            WHERE embedded_at IS NULL AND content != '' AND is_revoked = FALSE
            ORDER BY timestamp ASC
            LIMIT %s
        """, (batch_size,))

        columns = [desc[0] for desc in cur.description]
        return [dict(zip(columns, row)) for row in cur.fetchall()]


def mark_embedded(pg_conn, message_ids: list[tuple[str, str]]):
    """Mark messages as embedded."""
    with pg_conn.cursor() as cur:
        for msg_id, chat_jid in message_ids:
            cur.execute(
                "UPDATE messages SET embedded_at = NOW() WHERE id = %s AND chat_jid = %s",
                (msg_id, chat_jid),
            )
    pg_conn.commit()


def embed_batch(model: SentenceTransformer, pg_conn, messages: list[dict]):
    """Embed a batch of messages and store in pgvector."""
    texts = [m["content"] for m in messages]
    embeddings = model.encode(texts, show_progress_bar=False)

    ids_to_mark = []
    with pg_conn.cursor() as cur:
        for msg, embedding in zip(messages, embeddings):
            vec_str = "[" + ",".join(str(float(v)) for v in embedding) + "]"

            cur.execute("""
                INSERT INTO message_embeddings
                    (message_id, chat_jid, sender, sender_name, content, timestamp, is_from_me, embedding)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s::vector)
                ON CONFLICT (message_id, chat_jid) DO NOTHING
            """, (
                msg["id"],
                msg["chat_jid"],
                msg["sender"],
                msg["sender_name"],
                msg["content"],
                msg["timestamp"],
                msg["is_from_me"],
                vec_str,
            ))
            ids_to_mark.append((msg["id"], msg["chat_jid"]))

    pg_conn.commit()
    mark_embedded(pg_conn, ids_to_mark)

    logger.info("embedded %d messages", len(messages))


def run_loop(pg_dsn: str, model_name: str, batch_size: int, poll_interval: int):
    """Main embedding loop."""
    logger.info("loading model '%s'...", model_name)
    model = SentenceTransformer(model_name)
    dim = model.get_sentence_embedding_dimension()
    logger.info("model loaded (dim=%d)", dim)

    pg_conn = psycopg2.connect(pg_dsn)
    ensure_pgvector(pg_conn)
    ensure_embedded_at_column(pg_conn)

    logger.info("starting embedding loop (batch=%d, poll=%ds)", batch_size, poll_interval)

    while running:
        try:
            messages = fetch_unembedded(pg_conn, batch_size)
            if messages:
                embed_batch(model, pg_conn, messages)
            else:
                time.sleep(poll_interval)
        except psycopg2.OperationalError:
            logger.warning("PostgreSQL connection lost, reconnecting...")
            try:
                pg_conn.close()
            except Exception:
                pass
            time.sleep(5)
            pg_conn = psycopg2.connect(pg_dsn)
        except Exception as e:
            logger.error("embedding error: %s", e, exc_info=True)
            time.sleep(poll_interval)

    pg_conn.close()
    logger.info("embedding worker stopped")


def main():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        stream=sys.stderr,
    )

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)

    parser = argparse.ArgumentParser(description="WhatsApp message embedding worker")
    parser.add_argument("--pg", default=os.environ.get("PG_DSN", "postgres://bridge:PASSWORD@localhost:5432/whatsapp?sslmode=disable"))
    parser.add_argument("--model", default=os.environ.get("EMBEDDING_MODEL", DEFAULT_MODEL))
    parser.add_argument("--batch-size", type=int, default=int(os.environ.get("BATCH_SIZE", DEFAULT_BATCH_SIZE)))
    parser.add_argument("--poll-interval", type=int, default=int(os.environ.get("POLL_INTERVAL", DEFAULT_POLL_INTERVAL)))
    args = parser.parse_args()

    run_loop(args.pg, args.model, args.batch_size, args.poll_interval)


if __name__ == "__main__":
    main()
