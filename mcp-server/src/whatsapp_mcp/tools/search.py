"""Semantic search tool: find messages by meaning using pgvector."""

import json
import os
import time

from whatsapp_mcp.server import mcp

PG_DSN = os.environ.get("PG_DSN", "")

# Lazy-loaded clients
_pg_conn = None
_model = None


def _get_pg():
    global _pg_conn
    if _pg_conn is None or _pg_conn.closed:
        import psycopg2
        _pg_conn = psycopg2.connect(PG_DSN)
    return _pg_conn


def _get_model():
    global _model
    if _model is None:
        from sentence_transformers import SentenceTransformer
        model_name = os.environ.get("EMBEDDING_MODEL", "paraphrase-multilingual-MiniLM-L12-v2")
        _model = SentenceTransformer(model_name)
    return _model


@mcp.tool()
async def semantic_search(
    query: str,
    limit: int = 10,
    chat_jid: str | None = None,
    sender: str | None = None,
) -> str:
    """Search messages by meaning using vector similarity.

    Finds messages semantically similar to the query, even if exact words differ.
    Requires the embedding worker to be running (whatsapp-embedder).

    Args:
        query: Natural language search query
        limit: Max results to return (default 10)
        chat_jid: Optional filter by chat JID
        sender: Optional filter by sender JID
    """
    start = time.monotonic()
    try:
        conn = _get_pg()
        model = _get_model()

        # Embed the query
        query_vector = model.encode(query).tolist()
        vec_str = "[" + ",".join(str(float(v)) for v in query_vector) + "]"

        # Build query with optional filters
        where_clauses = []
        params = [vec_str, limit]

        if chat_jid:
            where_clauses.append("chat_jid = %s")
            params.insert(-1, chat_jid)
        if sender:
            where_clauses.append("sender = %s")
            params.insert(-1, sender)

        where_sql = ""
        if where_clauses:
            where_sql = "WHERE " + " AND ".join(where_clauses)

        sql = f"""
            SELECT
                1 - (embedding <=> %s::vector) as score,
                content, sender_name, chat_jid, timestamp, message_id
            FROM message_embeddings
            {where_sql}
            ORDER BY embedding <=> %s::vector
            LIMIT %s
        """

        # We need the vector param twice (for score calc and ordering)
        final_params = [vec_str]
        if chat_jid:
            final_params.append(chat_jid)
        if sender:
            final_params.append(sender)
        final_params.append(vec_str)
        final_params.append(limit)

        with conn.cursor() as cur:
            cur.execute(sql, final_params)
            rows = cur.fetchall()

        if not rows:
            return "No matching messages found."

        matches = []
        for row in rows:
            matches.append({
                "score": round(float(row[0]), 4),
                "content": row[1],
                "sender_name": row[2],
                "chat_jid": row[3],
                "timestamp": row[4],
                "message_id": row[5],
            })

        duration = int((time.monotonic() - start) * 1000)
        return json.dumps({"count": len(matches), "query": query, "duration_ms": duration, "matches": matches}, indent=2)

    except ImportError:
        return "Error: sentence-transformers or psycopg2 not installed in MCP server environment"
    except Exception as e:
        # Reset connection on error
        global _pg_conn
        try:
            _pg_conn.close()
        except Exception:
            pass
        _pg_conn = None
        return f"Error: {e}"
