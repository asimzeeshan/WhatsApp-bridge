"""Group tools: list and get."""

import json
import time

from whatsapp_mcp.server import mcp, bridge, registry


def _get_bridge(account: str | None = None):
    if account:
        return registry.get(account)
    return bridge


@mcp.tool()
async def list_groups(account: str | None = None) -> str:
    """Return all WhatsApp group chats the account belongs to.

    Args:
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        result = await b.get("/api/groups")
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("list_groups", duration, True)
        return json.dumps(result, indent=2)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("list_groups", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def get_group(jid: str, account: str | None = None) -> str:
    """Get group metadata including participant list and admin roles.

    Args:
        jid: Group JID ending with @g.us
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        if not jid.endswith("@g.us"):
            return "Error: jid must end with @g.us"
        result = await b.get(f"/api/groups/{jid}")
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("get_group", duration, True)
        return json.dumps(result, indent=2)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("get_group", duration, False, str(e))
        return f"Error: {e}"
