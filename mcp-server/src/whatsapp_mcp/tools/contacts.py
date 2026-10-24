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


@mcp.tool()
async def get_profile_picture(
    jid: str = "me",
    download: bool = True,
    preview: bool = False,
    output_dir: str | None = None,
    account: str | None = None,
) -> str:
    """Get the WhatsApp profile picture of a user, a group, or this account.

    Returns has_picture plus picture_id/url. has_picture is false with
    reason "not_set" (no picture) or "hidden" (privacy settings).
    With download=true the image is saved and file_path is returned,
    so it can be opened and looked at.

    Args:
        jid: Contact or group JID, or "me" for this account's own picture
        download: Save the image to disk and return file_path (default true)
        preview: Fetch the small thumbnail instead of full resolution
        output_dir: Directory to save the image (default: bridge's ./media/profile)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        params = {
            "download": "true" if download else "false",
            "preview": "true" if preview else "false",
        }
        if output_dir:
            params["output_dir"] = output_dir
        result = await b.get(f"/api/profile-picture/{jid}", params=params)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("get_profile_picture", duration, True)
        return json.dumps(result, indent=2)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("get_profile_picture", duration, False, str(e))
        return f"Error: {e}"
