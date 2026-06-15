"""Transcription worker: polls PostgreSQL for untranscribed audio, sends to Whisper, updates DB.

Handles retries gracefully - if Whisper is down, backs off and retries next cycle.
If audio file is missing on disk, attempts re-download via bridge API.

Usage:
    whatsapp-transcriber --pg "$PG_DSN" --whisper "$WHISPER_URL"

Environment variables (alternative to CLI args):
    PG_DSN - PostgreSQL connection string
    WHISPER_URL - Whisper API endpoint
    WHISPER_MODEL - Model name (default: large-v3)
    BRIDGE_URL - Bridge API for re-downloading media (default: http://127.0.0.1:8080)
    BATCH_SIZE - Messages per cycle (default: 20)
    POLL_INTERVAL - Seconds between polls (default: 30)
"""

import argparse
import logging
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time

import httpx
import psycopg2

logger = logging.getLogger(__name__)

DEFAULT_WHISPER_URL = "http://127.0.0.1:8443"
DEFAULT_WHISPER_MODEL = "large-v3"
DEFAULT_BRIDGE_URL = "http://127.0.0.1:8080"
DEFAULT_BATCH_SIZE = 20
DEFAULT_POLL_INTERVAL = 30

running = True
whisper_available = True
consecutive_failures = 0
MAX_BACKOFF = 300  # 5 minutes max backoff


def handle_signal(signum, frame):
    global running
    logger.info("received signal %s, shutting down...", signum)
    running = False


def fetch_untranscribed(pg_conn, batch_size: int) -> list[dict]:
    """Fetch audio messages that need transcription.

    Skips rows whose download has permanently failed - those are expired
    WhatsApp media (>14 days old) that no number of retries will recover.
    """
    with pg_conn.cursor() as cur:
        cur.execute("""
            SELECT mm.message_id AS id, mm.chat_jid,
                   mm.local_path, mm.media_url, mm.direct_path,
                   mm.file_length, mm.mime_type
            FROM messages_media mm
            JOIN messages m ON m.id = mm.message_id AND m.chat_jid = mm.chat_jid
            WHERE mm.media_type = 'audio'
              AND mm.transcribed_at IS NULL
              AND NOT mm.download_permanently_failed
              AND m.is_revoked = FALSE
            ORDER BY m.timestamp DESC
            LIMIT %s
        """, (batch_size,))

        columns = [desc[0] for desc in cur.description]
        return [dict(zip(columns, row)) for row in cur.fetchall()]


def record_redownload_failure(pg_conn, msg_id: str, chat_jid: str, error_text: str):
    """Bump download_attempts; flip permanent_failed once attempts >= 8 or on a
    terminal HTTP code so we stop hammering bridge/WhatsApp for dead media."""
    permanent_patterns = ("status code 403", "status code 404", "status code 410",
                          "MISSING_MEDIA_INFO")
    permanent = any(p in error_text for p in permanent_patterns)
    with pg_conn.cursor() as cur:
        cur.execute(
            """
            UPDATE messages_media SET
                download_attempts = CASE WHEN %s THEN 8
                                         ELSE download_attempts + 1 END,
                download_last_attempt_at = NOW(),
                download_last_error = LEFT(%s, 500),
                download_permanently_failed = CASE
                    WHEN %s THEN TRUE
                    WHEN download_attempts + 1 >= 8 THEN TRUE
                    ELSE download_permanently_failed END
            WHERE message_id = %s AND chat_jid = %s
            """,
            (permanent, error_text or '', permanent, msg_id, chat_jid),
        )
    pg_conn.commit()


def normalize_mime_type(mime_type: str | None) -> str:
    """Strip MIME parameters so downstream consumers get a plain content type."""
    if not mime_type:
        return "audio/ogg"
    return mime_type.split(";", 1)[0].strip() or "audio/ogg"


def check_whisper(whisper_url: str) -> bool:
    """Check if Whisper API is reachable."""
    try:
        resp = httpx.get(f"{whisper_url}/", timeout=5.0)
        return resp.status_code < 500
    except Exception:
        return False


def convert_to_wav(file_path: str) -> str | None:
    """Convert audio to WAV using opusdec (preferred) or afconvert (macOS fallback).

    whisper.cpp requires WAV input - it rejects raw ogg/opus.
    Returns path to temporary WAV file, or None on failure.
    """
    wav_path = tempfile.mktemp(suffix=".wav")

    # Try opusdec first (handles ogg/opus natively)
    if shutil.which("opusdec"):
        try:
            subprocess.run(
                ["opusdec", file_path, wav_path],
                capture_output=True, timeout=30,
            )
            if os.path.isfile(wav_path) and os.path.getsize(wav_path) > 0:
                return wav_path
        except Exception as e:
            logger.debug("opusdec failed: %s", e)

    # Fallback: afconvert (macOS built-in)
    if shutil.which("afconvert"):
        try:
            subprocess.run(
                ["afconvert", "-f", "WAVE", "-d", "LEI16@16000", file_path, wav_path],
                capture_output=True, timeout=30,
            )
            if os.path.isfile(wav_path) and os.path.getsize(wav_path) > 0:
                return wav_path
        except Exception as e:
            logger.debug("afconvert failed: %s", e)

    # Clean up on failure
    if os.path.isfile(wav_path):
        os.unlink(wav_path)
    return None


def transcribe_file(file_path: str, whisper_url: str, model: str, mime_type: str | None = None) -> str | None:
    """Convert audio to WAV and send to whisper.cpp /inference endpoint."""
    if not os.path.isfile(file_path):
        return None

    wav_path = convert_to_wav(file_path)
    if not wav_path:
        logger.warning("WAV conversion failed for %s", file_path)
        return None

    try:
        with open(wav_path, "rb") as f:
            resp = httpx.post(
                f"{whisper_url}/inference",
                files={"file": (os.path.basename(wav_path), f, "audio/wav")},
                data={"temperature": "0.0", "response_format": "json",
                      "language": os.environ.get("WHISPER_LANGUAGE", "ur")},
                timeout=120.0,
            )
            resp.raise_for_status()
            data = resp.json()
            return data.get("text", "").strip()
    except Exception as e:
        logger.warning("transcription failed: %s", e)
        return None
    finally:
        if os.path.isfile(wav_path):
            os.unlink(wav_path)


def redownload_audio(msg_id: str, chat_jid: str, bridge_url: str, output_dir: str) -> tuple[str | None, str]:
    """Re-download audio via bridge API if local file is missing.

    Returns (file_path, error_text). On success: ("/some/path", "").
    On failure: (None, "<error string>"). The error text is fed to
    record_redownload_failure so we can flip download_permanently_failed
    on terminal HTTP codes (403/404/410) and stop hammering dead media.
    """
    try:
        os.makedirs(output_dir, exist_ok=True)
        resp = httpx.post(
            f"{bridge_url}/api/download",
            json={"message_id": msg_id, "chat_jid": chat_jid, "output_dir": output_dir},
            timeout=60.0,
        )
        if resp.status_code >= 400:
            # Surface the bridge's response body - it contains the WhatsApp HTTP code
            # we use to classify permanent failures.
            try:
                body = resp.json()
                err = f"HTTP {resp.status_code}: {body}"
            except Exception:
                err = f"HTTP {resp.status_code}: {resp.text[:200]}"
            return (None, err)
        data = resp.json()
        return (data.get("file_path"), "")
    except Exception as e:
        return (None, f"request_error: {e}")


def update_transcription(pg_conn, msg_id: str, chat_jid: str, text: str):
    """Write transcription back to messages_media."""
    with pg_conn.cursor() as cur:
        cur.execute(
            """UPDATE messages_media SET transcription = %s, transcribed_at = NOW()
               WHERE message_id = %s AND chat_jid = %s""",
            (text, msg_id, chat_jid),
        )
    pg_conn.commit()


def update_local_path(pg_conn, msg_id: str, chat_jid: str, local_path: str):
    """After a successful re-download, persist the new path."""
    with pg_conn.cursor() as cur:
        cur.execute(
            """UPDATE messages_media SET local_path = %s, downloaded_at = NOW()
               WHERE message_id = %s AND chat_jid = %s""",
            (local_path, msg_id, chat_jid),
        )
    pg_conn.commit()


def run_loop(pg_dsn: str, whisper_url: str, model: str, bridge_url: str,
             batch_size: int, poll_interval: int):
    """Main transcription loop."""
    global whisper_available, consecutive_failures

    pg_conn = psycopg2.connect(pg_dsn)
    redownload_dir = "/tmp/whatsapp-transcribe"

    logger.info("transcription worker started (whisper=%s, model=%s, poll=%ds)",
                whisper_url, model, poll_interval)

    while running:
        try:
            # Check Whisper availability periodically
            if not whisper_available or consecutive_failures >= 3:
                if check_whisper(whisper_url):
                    if not whisper_available:
                        logger.info("Whisper is back online")
                    whisper_available = True
                    consecutive_failures = 0
                else:
                    if whisper_available:
                        logger.warning("Whisper is offline, will retry")
                    whisper_available = False
                    backoff = min(poll_interval * (2 ** consecutive_failures), MAX_BACKOFF)
                    time.sleep(backoff)
                    continue

            messages = fetch_untranscribed(pg_conn, batch_size)
            if not messages:
                time.sleep(poll_interval)
                continue

            transcribed = 0
            failed = 0

            for msg in messages:
                if not running:
                    break

                msg_id = msg["id"]
                chat_jid = msg["chat_jid"]
                local_path = msg.get("local_path", "")
                mime_type = msg.get("mime_type", "")

                # Try local file first
                if not local_path or not os.path.isfile(local_path):
                    # Re-download via bridge API
                    new_path, err = redownload_audio(msg_id, chat_jid, bridge_url, redownload_dir)
                    if new_path:
                        local_path = new_path
                        update_local_path(pg_conn, msg_id, chat_jid, local_path)
                    else:
                        # Record so we stop re-trying dead media every cycle
                        record_redownload_failure(pg_conn, msg_id, chat_jid, err)
                        logger.debug("skip %s - re-download failed: %s", msg_id, err)
                        failed += 1
                        continue

                text = transcribe_file(local_path, whisper_url, model, mime_type)
                if text is None:
                    consecutive_failures += 1
                    failed += 1
                    if consecutive_failures >= 3:
                        logger.warning("3 consecutive failures, backing off")
                        break
                    continue

                consecutive_failures = 0

                if text:
                    update_transcription(pg_conn, msg_id, chat_jid, text)
                    logger.info("transcribed %s: %s", msg_id, text[:80])
                    transcribed += 1
                else:
                    # Empty transcription (silence) - mark as transcribed to avoid retrying
                    update_transcription(pg_conn, msg_id, chat_jid, "[silence]")
                    transcribed += 1

            if transcribed > 0 or failed > 0:
                logger.info("cycle: %d transcribed, %d failed", transcribed, failed)

            if not messages or failed == len(messages):
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
            logger.error("worker error: %s", e, exc_info=True)
            time.sleep(poll_interval)

    pg_conn.close()
    logger.info("transcription worker stopped")


def main():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        stream=sys.stderr,
    )

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)

    parser = argparse.ArgumentParser(description="WhatsApp voice note transcription worker")
    parser.add_argument("--pg", default=os.environ.get("PG_DSN", ""))
    parser.add_argument("--whisper", default=os.environ.get("WHISPER_URL", DEFAULT_WHISPER_URL))
    parser.add_argument("--model", default=os.environ.get("WHISPER_MODEL", DEFAULT_WHISPER_MODEL))
    parser.add_argument("--bridge", default=os.environ.get("BRIDGE_URL", DEFAULT_BRIDGE_URL))
    parser.add_argument("--batch-size", type=int, default=int(os.environ.get("BATCH_SIZE", DEFAULT_BATCH_SIZE)))
    parser.add_argument("--poll-interval", type=int, default=int(os.environ.get("POLL_INTERVAL", DEFAULT_POLL_INTERVAL)))
    args = parser.parse_args()

    run_loop(args.pg, args.whisper, args.model, args.bridge, args.batch_size, args.poll_interval)


if __name__ == "__main__":
    main()
