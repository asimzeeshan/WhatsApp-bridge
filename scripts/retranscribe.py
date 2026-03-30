#!/usr/bin/env python3
"""Re-transcribe all audio messages that have no transcription yet.
Uses only Python stdlib - no pip dependencies needed."""

import json
import os
import sqlite3
import urllib.request
import urllib.error
from io import BytesIO

DB_PATH = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data", "whatsapp.db")
WHISPER_URL = "http://127.0.0.1:8443/v1/audio/transcriptions"
BRIDGE_URL = "http://127.0.0.1:8080"
MODEL = "Systran/faster-whisper-medium"


def get_audio_messages(conn):
    cursor = conn.execute("""
        SELECT id, chat_jid, sender_name, timestamp, file_length, content
        FROM messages WHERE media_type = 'audio'
        ORDER BY timestamp ASC
    """)
    return cursor.fetchall()


def download_media(message_id, chat_jid):
    body = json.dumps({
        "message_id": message_id,
        "chat_jid": chat_jid,
        "output_dir": "/tmp/whatsapp-transcribe"
    }).encode()

    req = urllib.request.Request(
        f"{BRIDGE_URL}/api/download",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            data = json.loads(resp.read())
            return data.get("file_path")
    except urllib.error.URLError as e:
        print(f"  Download failed: {e}")
        return None


def transcribe(file_path):
    boundary = "----WhatsAppBridgeBoundary"
    filename = os.path.basename(file_path)

    with open(file_path, "rb") as f:
        file_data = f.read()

    body = BytesIO()
    # File field
    body.write(f"--{boundary}\r\n".encode())
    body.write(f'Content-Disposition: form-data; name="file"; filename="{filename}"\r\n'.encode())
    body.write(b"Content-Type: audio/ogg\r\n\r\n")
    body.write(file_data)
    body.write(b"\r\n")
    # Model field
    body.write(f"--{boundary}\r\n".encode())
    body.write(b'Content-Disposition: form-data; name="model"\r\n\r\n')
    body.write(MODEL.encode())
    body.write(b"\r\n")
    # Response format
    body.write(f"--{boundary}\r\n".encode())
    body.write(b'Content-Disposition: form-data; name="response_format"\r\n\r\n')
    body.write(b"json")
    body.write(b"\r\n")
    body.write(f"--{boundary}--\r\n".encode())

    req = urllib.request.Request(
        WHISPER_URL,
        data=body.getvalue(),
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        method="POST"
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            data = json.loads(resp.read())
            return data.get("text", "")
    except urllib.error.URLError as e:
        print(f"  Whisper failed: {e}")
        return None
    except Exception as e:
        print(f"  Whisper error: {e}")
        return None


def main():
    os.makedirs("/tmp/whatsapp-transcribe", exist_ok=True)

    conn = sqlite3.connect(DB_PATH)
    messages = get_audio_messages(conn)

    print(f"Found {len(messages)} audio messages")
    transcribed = 0
    skipped = 0
    failed = 0

    for msg_id, chat_jid, sender, ts, size, content in messages:
        # Check if already transcribed
        existing = conn.execute(
            "SELECT transcription FROM messages WHERE id = ? AND chat_jid = ?",
            (msg_id, chat_jid)
        ).fetchone()
        if existing and existing[0]:
            print(f"  Skip {msg_id} - already transcribed")
            skipped += 1
            continue

        print(f"  Processing {msg_id} from {sender} ({size} bytes)...")

        file_path = download_media(msg_id, chat_jid)
        if not file_path:
            failed += 1
            continue

        text = transcribe(file_path)
        if text is None:
            failed += 1
            continue

        if text.strip():
            conn.execute(
                "UPDATE messages SET transcription = ? WHERE id = ? AND chat_jid = ?",
                (text, msg_id, chat_jid)
            )
            conn.commit()
            print(f"    OK: {text[:80]}...")
            transcribed += 1
        else:
            print(f"    Empty transcription")
            failed += 1

        try:
            os.remove(file_path)
        except OSError:
            pass

    conn.close()
    print(f"\nDone: {transcribed} transcribed, {skipped} skipped, {failed} failed")


if __name__ == "__main__":
    main()
