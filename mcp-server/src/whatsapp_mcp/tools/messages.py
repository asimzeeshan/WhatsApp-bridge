"""Message tools: send, react, edit, revoke, check, get, unread."""

import json
import re
import time

from whatsapp_mcp.server import mcp, bridge, registry


def _normalize_phone(phone: str) -> str:
    """Normalize phone number to JID format."""
    if "@" in phone:
        return phone
    digits = re.sub(r"\D", "", phone)
    if len(digits) < 10:
        raise ValueError(f"Phone number too short: {phone}")
    return f"{digits}@s.whatsapp.net"


def _get_bridge(account: str | None = None):
    """Get bridge client, defaulting to primary account."""
    if account:
        return registry.get(account)
    return bridge


def _get_send_bridge(account: str | None = None):
    """Get bridge client for send operations (enforces read-only)."""
    if account:
        return registry.get_for_send(account)
    return registry.get_for_send()


@mcp.tool()
async def send_message(
    to: str,
    text: str,
    mentions: list[str] | None = None,
    quotedMessageId: str | None = None,
    quotedParticipant: str | None = None,
    account: str | None = None,
) -> str:
    """Send a text message to an individual contact.

    Args:
        to: Phone number with country code (e.g. 923001234567) or JID
        text: Message text to send
        mentions: Optional JIDs to mention (include @number in text)
        quotedMessageId: Optional message ID to reply to
        quotedParticipant: Optional sender JID of quoted message
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_send_bridge(account)
        jid = _normalize_phone(to)
        body = {"to": jid, "text": text}
        if mentions:
            body["mentions"] = mentions
        if quotedMessageId:
            body["quotedMessageId"] = quotedMessageId
        if quotedParticipant:
            body["quotedParticipant"] = quotedParticipant

        result = await b.post("/api/send", json=body)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("send_message", duration, True)
        return f"Message sent successfully to {jid}. ID: {result.get('message_id', 'unknown')}"
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("send_message", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def send_group_message(
    jid: str,
    text: str,
    mentions: list[str] | None = None,
    quotedMessageId: str | None = None,
    quotedParticipant: str | None = None,
    account: str | None = None,
) -> str:
    """Send a text message to a WhatsApp group.

    Args:
        jid: Group JID ending with @g.us
        text: Message text to send
        mentions: Optional JIDs to mention
        quotedMessageId: Optional message ID to reply to
        quotedParticipant: Optional sender JID of quoted message
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_send_bridge(account)
        if not jid.endswith("@g.us"):
            return "Error: jid must end with @g.us"

        body = {"to": jid, "text": text}
        if mentions:
            body["mentions"] = mentions
        if quotedMessageId:
            body["quotedMessageId"] = quotedMessageId
        if quotedParticipant:
            body["quotedParticipant"] = quotedParticipant

        result = await b.post("/api/send", json=body)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("send_group_message", duration, True)
        return f"Message sent to group {jid}. ID: {result.get('message_id', 'unknown')}"
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("send_group_message", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def send_reaction(
    chat_jid: str,
    message_id: str,
    emoji: str,
    sender: str | None = None,
    account: str | None = None,
) -> str:
    """Send an emoji reaction to a message.

    Args:
        chat_jid: Chat JID (group or individual)
        message_id: ID of the message to react to
        emoji: Emoji to react with (empty string to remove reaction)
        sender: Optional sender JID of the target message
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_send_bridge(account)
        body = {"chat_jid": chat_jid, "message_id": message_id, "emoji": emoji}
        if sender:
            body["sender"] = sender

        result = await b.post("/api/send/reaction", json=body)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("send_reaction", duration, True)
        return f"Reaction '{emoji}' sent. ID: {result.get('message_id', 'unknown')}"
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("send_reaction", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def edit_message(
    chat_jid: str,
    message_id: str,
    new_text: str,
    account: str | None = None,
) -> str:
    """Edit a previously sent message (own messages only, within 20 minutes).

    Args:
        chat_jid: Chat JID where the message was sent
        message_id: ID of the message to edit
        new_text: New text content for the message
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_send_bridge(account)
        body = {"chat_jid": chat_jid, "message_id": message_id, "new_text": new_text}
        result = await b.post("/api/send/edit", json=body)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("edit_message", duration, True)
        return f"Message edited. ID: {result.get('message_id', 'unknown')}"
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("edit_message", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def revoke_message(
    chat_jid: str,
    message_id: str,
    sender: str | None = None,
    account: str | None = None,
) -> str:
    """Delete/revoke a message for everyone in the chat.

    Args:
        chat_jid: Chat JID where the message was sent
        message_id: ID of the message to revoke
        sender: Optional sender JID (for admin revoking others' messages)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_send_bridge(account)
        body = {"chat_jid": chat_jid, "message_id": message_id}
        if sender:
            body["sender"] = sender

        result = await b.post("/api/send/revoke", json=body)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("revoke_message", duration, True)
        return f"Message revoked. ID: {result.get('message_id', 'unknown')}"
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("revoke_message", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def check_new_messages(
    jid: str,
    limit: int = 100,
    account: str | None = None,
) -> str:
    """Check for new messages in a chat since last check.

    Server-side watermarks tracked in the bridge (persisted in database).
    Returns only unseen messages.

    Args:
        jid: Chat JID to check (group or individual)
        limit: Max messages to return (default 100)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        result = await b.get("/api/check", params={"jid": jid, "limit": str(limit)})
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("check_new_messages", duration, True)

        messages = result.get("messages", [])
        if not messages:
            return "No new messages."
        return json.dumps({"count": len(messages), "messages": messages})
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("check_new_messages", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def check_triggers(
    jids: list[str],
    mention_jid: str | None = None,
    sender_jids: list[str] | None = None,
    limit: int = 100,
    account: str | None = None,
) -> str:
    """Check multiple chats for new messages in a single call.

    Batch version of check_new_messages - checks all JIDs at once using
    per-JID watermarks. Returns only unseen messages, grouped by JID.

    Args:
        jids: List of chat JIDs to check (groups, individuals, or LIDs)
        mention_jid: Optional JID to filter messages mentioning this user
        sender_jids: Optional list of sender JIDs to include regardless of mentions
        limit: Max messages per JID (default 100)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        body: dict = {"jids": jids, "limit": limit, "filters": {}}
        if mention_jid:
            body["filters"]["mention_jid"] = mention_jid
        if sender_jids:
            body["filters"]["sender_jids"] = sender_jids

        result = await b.post("/api/check/triggers", json=body)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("check_triggers", duration, True)

        total = result.get("total", 0)
        if total == 0:
            return "No new messages."
        return json.dumps(result)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("check_triggers", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def get_messages(
    jid: str,
    limit: int = 20,
    account: str | None = None,
) -> str:
    """Retrieve recent messages from a WhatsApp chat.

    Args:
        jid: Chat JID (e.g. 923001234567@s.whatsapp.net or groupid@g.us)
        limit: Max messages to return (default 20)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        result = await b.get("/api/messages", params={"chat_jid": jid, "limit": str(limit)})
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("get_messages", duration, True)
        return json.dumps(result)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("get_messages", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def get_unread_chats(
    messageLimit: int = 5,
    account: str | None = None,
) -> str:
    """Return all chats with unread messages, with recent messages per chat.

    Args:
        messageLimit: Max messages per unread chat (default 5)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        result = await b.get("/api/unread", params={"msg_limit": str(messageLimit)})
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("get_unread_chats", duration, True)

        chats = result.get("chats", [])
        if not chats:
            return "No unread chats."
        return json.dumps(result, indent=2)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("get_unread_chats", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def get_unread_messages(account: str | None = None) -> str:
    """Return a flat list of all unread messages across all chats, newest first.

    Args:
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        result = await b.get("/api/unread", params={"flat": "true"})
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("get_unread_messages", duration, True)

        messages = result.get("messages", [])
        if not messages:
            return "No unread messages."
        return json.dumps(result, indent=2)
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("get_unread_messages", duration, False, str(e))
        return f"Error: {e}"
