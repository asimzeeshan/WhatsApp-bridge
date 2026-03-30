"""Media tools: send and download."""

import json
import os
import time

from whatsapp_mcp.server import mcp, bridge, registry


def _get_send_bridge(account: str | None = None):
    if account:
        return registry.get_for_send(account)
    return registry.get_for_send()


def _get_bridge(account: str | None = None):
    if account:
        return registry.get(account)
    return bridge


@mcp.tool()
async def send_media(
    to: str,
    filePath: str,
    caption: str | None = None,
    mediaType: str | None = None,
    ptt: bool = False,
    account: str | None = None,
) -> str:
    """Send a media file (image, video, audio, document) to a contact or group.

    Args:
        to: Phone number or JID (supports both individual and group)
        filePath: Absolute path to the file on disk
        caption: Optional caption text
        mediaType: Optional override (image/video/audio/document). Auto-detected if omitted.
        ptt: Set True for voice notes (requires OGG Opus format)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_send_bridge(account)
        if not os.path.isfile(filePath):
            return f"Error: file not found at {filePath}"

        data = {
            "to": to,
        }
        if caption:
            data["caption"] = caption
        if mediaType:
            data["media_type"] = mediaType
        if ptt:
            data["ptt"] = "true"

        with open(filePath, "rb") as f:
            files = {"file": (os.path.basename(filePath), f)}
            result = await b.post_multipart("/api/send/media", data=data, files=files)

        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("send_media", duration, True)
        return f"Media sent to {to}. ID: {result.get('message_id', 'unknown')}"
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("send_media", duration, False, str(e))
        return f"Error: {e}"


@mcp.tool()
async def download_media(
    messageId: str,
    jid: str,
    outputDir: str | None = None,
    account: str | None = None,
) -> str:
    """Download media from a received WhatsApp message.

    Args:
        messageId: The message ID containing media
        jid: Chat JID the message belongs to
        outputDir: Directory to save the file (default: ./downloads)
        account: Optional account name (for multi-account setups)
    """
    start = time.monotonic()
    try:
        b = _get_bridge(account)
        body = {
            "message_id": messageId,
            "chat_jid": jid,
        }
        if outputDir:
            body["output_dir"] = outputDir

        result = await b.post("/api/download", json=body)
        duration = int((time.monotonic() - start) * 1000)
        await b.record_tool_call("download_media", duration, True)
        return f"Downloaded to {result.get('file_path')} ({result.get('file_size', 0)} bytes, type: {result.get('media_type')})"
    except Exception as e:
        duration = int((time.monotonic() - start) * 1000)
        await bridge.record_tool_call("download_media", duration, False, str(e))
        return f"Error: {e}"
