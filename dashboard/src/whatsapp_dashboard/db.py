"""Database connection for the dashboard. Supports SQLite and PostgreSQL.

Configuration priority:
1. Environment variables: DB_DRIVER, PG_DSN, DB_PATH
2. Auto-detect from config.toml (reads bridge.database.driver and bridge.database.dsn)
3. Default: SQLite at data/whatsapp.db
"""

import os
import sqlite3
from contextlib import contextmanager
from pathlib import Path

_PROJECT_ROOT = Path(__file__).resolve().parent.parent.parent.parent
_DEFAULT_DB = str(_PROJECT_ROOT / "data" / "whatsapp.db")
_CONFIG_PATH = _PROJECT_ROOT / "config.toml"


def _detect_config():
    """Read database config from config.toml if env vars not set."""
    driver = os.environ.get("DB_DRIVER", "")
    pg_dsn = os.environ.get("PG_DSN", "")

    if driver:
        return driver, pg_dsn

    # Try to read from config.toml
    if _CONFIG_PATH.exists():
        try:
            import tomllib
        except ImportError:
            try:
                import tomli as tomllib
            except ImportError:
                return "sqlite", ""

        with open(_CONFIG_PATH, "rb") as f:
            cfg = tomllib.load(f)

        db_cfg = cfg.get("bridge", {}).get("database", {})
        driver = db_cfg.get("driver", "sqlite")
        pg_dsn = db_cfg.get("dsn", "")
        return driver, pg_dsn

    return "sqlite", ""


DB_DRIVER, PG_DSN = _detect_config()
DB_PATH = os.environ.get("DB_PATH", _DEFAULT_DB)

# Lazy-loaded PostgreSQL connection pool
_pg_pool = None


def _get_pg_pool():
    global _pg_pool
    if _pg_pool is None:
        import psycopg2
        import psycopg2.pool
        _pg_pool = psycopg2.pool.ThreadedConnectionPool(1, 5, PG_DSN)
    return _pg_pool


@contextmanager
def get_db():
    """Open a database connection (SQLite read-only or PostgreSQL)."""
    if DB_DRIVER == "postgres":
        pool = _get_pg_pool()
        conn = pool.getconn()
        try:
            yield conn
        finally:
            pool.putconn(conn)
    else:
        conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
        conn.row_factory = sqlite3.Row
        try:
            yield conn
        finally:
            conn.close()


def query(sql: str, params: tuple = ()) -> list[dict]:
    """Execute a read-only query and return results as dicts."""
    with get_db() as conn:
        if DB_DRIVER == "postgres":
            import psycopg2.extras
            cursor = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
            cursor.execute(sql, params)
            return [dict(row) for row in cursor.fetchall()]
        else:
            cursor = conn.execute(sql, params)
            columns = [desc[0] for desc in cursor.description]
            return [dict(zip(columns, row)) for row in cursor.fetchall()]


def query_one(sql: str, params: tuple = ()) -> dict | None:
    """Execute a query and return a single result or None."""
    results = query(sql, params)
    return results[0] if results else None
