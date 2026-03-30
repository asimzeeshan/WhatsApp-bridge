"""Contact tools: list and get."""

import json
import time

from whatsapp_mcp.server import mcp, bridge, registry


def _get_bridge(account: str | None = None):
    if account:
        return registry.get(account)
    return bridge


@mcp.tool()
async def list_contacts(account: str | None = None) -> str:
    """Return all known WhatsApp contacts.

    Args:
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        result = await b.get("/api/contacts")
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("list_contacts", duration, True)
        return json.dumps(result, indent=2)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("list_contacts", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def get_contact(jid: str, account: str | None = None) -> str:
    """Get details for a single WhatsApp contact.

    Args:
        jid: Contact JID (e.g. 923001234567@s.whatsapp.net)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        result = await b.get(f"/api/contacts/{jid}")
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("get_contact", duration, True)
        return json.dumps(result, indent=2)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("get_contact", duration, False, str(e))
        return f"Error: {e}"
