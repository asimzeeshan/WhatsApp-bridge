"""FastAPI dashboard application."""

import os
from pathlib import Path

import httpx
from fastapi import FastAPI, Request
from fastapi.responses import HTMLResponse
from fastapi.templating import Jinja2Templates

from whatsapp_dashboard import db

BRIDGE_URL = os.environ.get("BRIDGE_URL", "http://127.0.0.1:8080")
TEMPLATES_DIR = Path(__file__).parent / "templates"

app = FastAPI(title="WhatsApp Bridge Dashboard")
templates = Jinja2Templates(directory=str(TEMPLATES_DIR))


async def fetch_bridge_status() -> dict:
    """Fetch status from the Go bridge."""
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            resp = await client.get(f"{BRIDGE_URL}/api/status")
            if resp.status_code == 200:
                return resp.json()
    except Exception:
        pass
    return {"state": "UNREACHABLE", "is_connected": False}


@app.get("/", response_class=HTMLResponse)
async def index(request: Request):
    """Main dashboard page."""
    status = await fetch_bridge_status()

    groups = db.query(
        "SELECT jid, name, is_group, unread_count, last_message_time, last_message_preview "
        "FROM chats WHERE is_group = TRUE ORDER BY last_message_time DESC"
    )

    recent_messages = db.query(
        "SELECT m.id, m.chat_jid, c.name as chat_name, m.sender, m.sender_name, "
        "m.content, m.timestamp, m.is_from_me, m.media_type, m.push_name, "
        "COALESCE(NULLIF(ct.name,''), NULLIF(ct.notify,''), NULLIF(m.push_name,''), NULLIF(m.sender_name,''), m.sender) as resolved_name "
        "FROM messages m LEFT JOIN chats c ON c.jid = m.chat_jid "
        "LEFT JOIN contacts ct ON ct.jid = m.sender "
        "ORDER BY m.timestamp DESC LIMIT 50"
    )

    telemetry = db.query_one(
        "SELECT * FROM telemetry_daily WHERE date = CAST(CURRENT_DATE AS TEXT)"
    ) or {
        "date": "today", "messages_sent": 0, "messages_received": 0,
        "media_downloaded": 0, "media_sent": 0, "links_indexed": 0,
    }

    links = db.query(
        "SELECT l.*, c.name as chat_name FROM links l "
        "LEFT JOIN chats c ON c.jid = l.chat_jid "
        "ORDER BY l.timestamp DESC LIMIT 30"
    )

    tool_calls = db.query(
        "SELECT * FROM telemetry_tool_calls ORDER BY called_at DESC LIMIT 20"
    )

    platform_counts = db.query(
        "SELECT platform, COUNT(*) as count FROM links GROUP BY platform ORDER BY count DESC"
    )

    return templates.TemplateResponse(
        request=request,
        name="index.html",
        context={
            "status": status,
            "groups": groups,
            "recent_messages": recent_messages,
            "telemetry": telemetry,
            "links": links,
            "tool_calls": tool_calls,
            "platform_counts": platform_counts,
        },
    )


# HTMX partial endpoints for auto-refresh

@app.get("/partials/status", response_class=HTMLResponse)
async def partial_status(request: Request):
    status = await fetch_bridge_status()
    return templates.TemplateResponse(
        request=request,
        name="partials/status.html",
        context={"status": status},
    )


@app.get("/partials/messages", response_class=HTMLResponse)
async def partial_messages(request: Request):
    recent_messages = db.query(
        "SELECT m.id, m.chat_jid, c.name as chat_name, m.sender, m.sender_name, "
        "m.content, m.timestamp, m.is_from_me, m.media_type, m.push_name, "
        "COALESCE(NULLIF(ct.name,''), NULLIF(ct.notify,''), NULLIF(m.push_name,''), NULLIF(m.sender_name,''), m.sender) as resolved_name "
        "FROM messages m LEFT JOIN chats c ON c.jid = m.chat_jid "
        "LEFT JOIN contacts ct ON ct.jid = m.sender "
        "ORDER BY m.timestamp DESC LIMIT 50"
    )
    return templates.TemplateResponse(
        request=request,
        name="partials/messages.html",
        context={"recent_messages": recent_messages},
    )


@app.get("/partials/telemetry", response_class=HTMLResponse)
async def partial_telemetry(request: Request):
    telemetry = db.query_one(
        "SELECT * FROM telemetry_daily WHERE date = CAST(CURRENT_DATE AS TEXT)"
    ) or {
        "date": "today", "messages_sent": 0, "messages_received": 0,
        "media_downloaded": 0, "media_sent": 0, "links_indexed": 0,
    }
    return templates.TemplateResponse(
        request=request,
        name="partials/telemetry.html",
        context={"telemetry": telemetry},
    )


def start():
    """CLI entry point."""
    import uvicorn
    uvicorn.run(app, host="127.0.0.1", port=9090)
