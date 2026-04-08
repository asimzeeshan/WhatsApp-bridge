# WhatsApp Bridge

Production-grade WhatsApp MCP server built with Go ([whatsmeow](https://github.com/tulir/whatsmeow)) + Python ([FastMCP](https://github.com/jlowin/fastmcp)).

Connect Claude Code (or any MCP-compatible AI) to WhatsApp - send messages, react, edit, delete, monitor groups, search by meaning, download media, transcribe voice notes, and more.

## Architecture

```
Claude Code <-stdio-> Python MCP Server <-HTTP-> Go Bridge <-WebSocket-> WhatsApp
                           |                        |
                     semantic_search           PostgreSQL / SQLite
                           |                        |
                     pgvector (in PG)       Python Dashboard (read-only)
                                            (FastAPI + HTMX)
```

**Components:**

- **Go Bridge** - WhatsApp protocol via whatsmeow, REST API, rate limiting, auto media download, link indexing, voice note transcription, telemetry
- **Python MCP Server** - 17 MCP tools over stdio, multi-account support
- **Python Dashboard** - Optional read-only web UI (FastAPI + HTMX)
- **Python Embedder** - Background worker that embeds messages into pgvector for semantic search
- **Python Transcriber** - Background worker that transcribes voice notes via Whisper with retry
- **PostgreSQL + pgvector** - Primary database with built-in vector search
- **SQLite** - Lightweight alternative for single-process setups (no vector search)

## Quick Start

### Option A: Docker (recommended)

```bash
git clone https://github.com/asimzeeshan/WhatsApp-bridge.git
cd WhatsApp-bridge
cp .env.example .env
cp config.example.toml config.toml
# Edit .env: set your POSTGRES_PASSWORD
# Edit config.toml: set bridge.database.driver = "postgres"

# Start core stack
docker compose up -d

# First-time QR scan
docker compose logs -f bridge
# Scan the QR code with WhatsApp > Settings > Linked Devices > Link a Device

# Optional: dashboard and whisper transcription
docker compose --profile dashboard --profile whisper up -d
```

Services started:
- **Bridge**: http://127.0.0.1:8080
- **PostgreSQL + pgvector**: 127.0.0.1:5432
- **Embedder**: background worker (auto-embeds new messages)
- **Transcriber**: background worker (auto-transcribes voice notes)
- **Dashboard** (optional): http://127.0.0.1:9090

### Option B: Native (macOS/Linux)

**Prerequisites:** Go 1.25+, Python 3.11+, [uv](https://github.com/astral-sh/uv)

```bash
git clone https://github.com/asimzeeshan/WhatsApp-bridge.git
cd WhatsApp-bridge
cp config.example.toml config.toml
```

First run (QR scan):
```bash
make build-bridge
make run-bridge       # scan QR code, then Ctrl+C
```

Run everything:
```bash
make run-all          # bridge + dashboard in background
make status           # check running status
make stop-all         # stop everything
```

> If your session expires (~20 days), run `make run-bridge` in the foreground to scan a new QR code.

### Connect Claude Code

Add to your `.mcp.json`:

```json
{
  "mcpServers": {
    "whatsapp-mcp": {
      "type": "stdio",
      "command": "uv",
      "args": ["run", "--directory", "/path/to/WhatsApp-bridge/mcp-server", "whatsapp-mcp"],
      "env": {
        "BRIDGE_URL": "http://127.0.0.1:8080"
      }
    }
  }
}
```

### Multi-account setup (optional)

Run multiple bridge instances for different WhatsApp accounts:

```bash
./bin/whatsapp-bridge --config config.primary.toml    # port 8080
./bin/whatsapp-bridge --config config.secondary.toml  # port 8081 (read-only monitoring)
```

Use `BRIDGE_ACCOUNTS` instead of `BRIDGE_URL`:

```json
{
  "env": {
    "BRIDGE_ACCOUNTS": "[{\"name\":\"primary\",\"url\":\"http://127.0.0.1:8080\"},{\"name\":\"secondary\",\"url\":\"http://127.0.0.1:8081\",\"read_only\":true}]"
  }
}
```

All MCP tools accept an optional `account` parameter. Send operations are blocked on read-only accounts.

## MCP Tools (17)

| Tool | Description |
|------|-------------|
| `send_message` | Send text to individual contact |
| `send_group_message` | Send text to group |
| `send_reaction` | React to a message with an emoji |
| `edit_message` | Edit a previously sent message |
| `revoke_message` | Delete/revoke a message for everyone |
| `check_new_messages` | Poll for new messages in a single chat (server-side watermarks) |
| `check_triggers` | Batch check multiple chats at once (single call, per-JID watermarks, dry_run support) |
| `get_messages` | Retrieve message history |
| `get_unread_chats` | List chats with unread messages |
| `get_unread_messages` | Flat list of all unread messages |
| `list_contacts` | List all contacts |
| `get_contact` | Get contact details |
| `list_groups` | List all groups |
| `get_group` | Get group metadata + participants |
| `send_media` | Send image/video/audio/document |
| `download_media` | Download media from received messages |
| `semantic_search` | Search messages by meaning (pgvector similarity) |

## Features

### Messaging
- **Text, media, voice notes** - Send and receive all message types
- **Emoji reactions** - Send and receive reactions on messages
- **Message editing** - Edit sent messages (within WhatsApp's 20-minute window)
- **Message revocation** - Delete messages for everyone
- **Quoting and mentions** - Reply to messages and @mention participants

### Media
- **Auto image download** - All received images saved to `media/images/YYYY-MM-DD/`
- **Auto audio download** - All received voice notes saved to `media/audio/YYYY-MM-DD/`
- **Media download API** - Download any media on demand via `download_media` tool or REST API
- **MIME type handling** - Correct file extensions even for parameterized types (e.g. `audio/ogg; codecs=opus`)
- **Local path tracking** - Downloaded media paths stored in database for transcription pipeline

### Storage
- **PostgreSQL + pgvector** - Production database with concurrent access and built-in vector search (recommended)
- **SQLite** - Lightweight alternative with WAL mode (single-process, no vector search)
- Config-driven: switch between SQLite and PostgreSQL via `config.toml`

### Intelligence
- **Semantic search** - Find messages by meaning using pgvector cosine similarity
- **Multilingual embeddings** - `paraphrase-multilingual-MiniLM-L12-v2` model (384 dimensions, 50+ languages)
- **Voice note transcription** - Automatic via Whisper with WAV conversion (supports whisper.cpp and OpenAI-compatible APIs)
- **Transcription retry** - Background worker with exponential backoff, re-download on missing files
- **Link indexing** - URLs extracted and categorized (YouTube, GitHub, Twitter/X, etc.)

### Operations
- **Multi-account** - Multiple WhatsApp accounts with read-only support
- **Rate limiting** - Configurable token bucket with jitter to prevent bans
- **Telemetry** - Daily message/media counters, per-tool-call tracking
- **Health endpoint** - Memory, disk, DB sizes, goroutine count
- **Dashboard** - Dark-themed HTMX UI with auto-refreshing status
- **Docker deployment** - Full stack with Compose, health checks, service dependencies
- **macOS launchd** - Auto-start on login, auto-restart on crash

## REST API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/status` | GET | Connection state and identity |
| `/api/health` | GET | Resource usage (memory, disk, DB sizes) |
| `/api/check?jid={jid}` | GET | New messages since last watermark (single JID) |
| `/api/check/triggers` | POST | Batch check multiple JIDs at once (dry_run support) |
| `/api/send` | POST | Send text message |
| `/api/send/media` | POST | Send media message |
| `/api/send/reaction` | POST | Send emoji reaction |
| `/api/send/edit` | POST | Edit a sent message |
| `/api/send/revoke` | POST | Revoke/delete a message |
| `/api/messages` | GET | Message history |
| `/api/contacts` | GET | All contacts |
| `/api/groups` | GET | All groups |
| `/api/links` | GET | Indexed links |
| `/api/telemetry/daily` | GET | Daily stats |
| `/api/download` | POST | Download media from a message |

## Configuration

Credentials live in `.env` (copy from `.env.example`). Bridge settings in `config.toml` (copy from `config.example.toml`).

```toml
[bridge]
addr = "127.0.0.1:8080"

[bridge.database]
driver = "postgres"  # or "sqlite"
# DSN set in .env as PG_DSN

[bridge.ratelimit]
messages_per_second = 0.5

[bridge.media]
auto_download_images = true
auto_download_audio = true
images_dir = "./media/images"
audio_dir = "./media/audio"

[bridge.transcription]
enabled = true
whisper_url = "http://127.0.0.1:8443/inference"
model = "large-v3-turbo"
language = ""  # empty = auto-detect
```

## Docker Services

| Service | Image | Port | Description |
|---------|-------|------|-------------|
| `bridge` | Built from `bridge/Dockerfile` | 8080 | Go WhatsApp bridge |
| `mcp-server` | Built from `mcp-server/Dockerfile` | - | MCP tools (stdio) |
| `postgres` | pgvector/pgvector:pg17 | 5432 | Database + vector search |
| `embedder` | Built from `embedder/Dockerfile` | - | Background embedding worker |
| `transcriber` | Built from `transcriber/Dockerfile` | - | Background transcription worker |
| `dashboard` | Built from `dashboard/Dockerfile` | 9090 | Web UI (optional, `--profile dashboard`) |
| `whisper` | speaches-ai/speaches | 8443 | Voice transcription (optional, `--profile whisper`) |

## Voice Note Transcription

The bridge supports automatic voice note transcription through two methods:

### In-bridge transcription
The Go bridge can transcribe voice notes inline as they arrive. Configure in `config.toml`:
```toml
[bridge.transcription]
enabled = true
whisper_url = "http://127.0.0.1:8443/inference"
```

### Standalone transcription worker
For more robust handling with retry logic, run the Python transcriber worker:
```bash
cd transcriber
uv run whatsapp-transcriber --pg "$PG_DSN" --whisper "$WHISPER_URL"
```

The worker:
- Polls PostgreSQL for untranscribed audio messages
- Converts ogg/opus to WAV using `opusdec` (preferred) or `afconvert` (macOS fallback)
- Sends WAV to whisper.cpp `/inference` endpoint
- Retries with exponential backoff when Whisper is unavailable
- Re-downloads audio via bridge API if local file is missing

### Whisper backends
- **whisper.cpp** (native) - Use `/inference` endpoint, requires WAV input and `temperature=0.0`
- **speaches-ai** (Docker) - OpenAI-compatible API at `/v1/audio/transcriptions`
- Any OpenAI-compatible Whisper API

## Ban Risk Assessment

This project uses **whatsmeow**, a reverse-engineered WhatsApp Web client. Using unofficial APIs carries inherent risk.

### Built-in Protections

| Protection | Implementation |
|------------|---------------|
| **Rate limiting** | Token bucket (0.5 msg/sec default) with random jitter |
| **Cooldown marker** | Ban detected -> `data/cooldown` file, 10 min pause |
| **Exponential backoff** | Reconnection: 1s -> 60s cap, with jitter |
| **Permanent disconnect** | TemporaryBan, LoggedOut, ClientOutdated all stop the bridge |
| **No bulk endpoints** | Single-message sends only |

### Recommendations

- Use a **secondary phone number**, not your primary
- Keep whatsmeow **updated** (`go get -u go.mau.fi/whatsmeow@latest`)
- Keep message volume **reasonable** (< 200 messages/day for automated sends)

## Security

- HTTP API bound to **127.0.0.1 only** (bridge and dashboard)
- All SQL queries use **parameterized statements**
- Path traversal protection on download directories
- Rate limiter enforced via chi middleware
- Dashboard is **read-only**
- Docker containers run as non-root (UID 1000)

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build bridge + install MCP + install dashboard |
| `make build-bridge` | Compile Go bridge binary |
| `make run-all` | Build and start bridge + dashboard in background |
| `make stop-all` | Stop all services |
| `make status` | Show running status |
| `make run-bridge` | Start bridge foreground (for QR scan) |
| `make logs-bridge` | Tail bridge logs |
| `make logs-dashboard` | Tail dashboard logs |
| `make test` | Run Go tests |
| `make lint` | Run Go vet |
| `make service-install` | Install macOS launchd service |
| `make service-restart` | Restart macOS launchd service |
| `make service-status` | Check macOS launchd service status |
| `make clean` | Remove binaries and database files |

## Project Structure

```
WhatsApp-bridge/
  bridge/            # Go bridge (whatsmeow + REST API)
    api/             #   HTTP handlers (send, download, status, health)
    client/          #   WhatsApp client wrapper (events, media download)
    config/          #   Configuration parsing
    media/           #   OGG analysis, Whisper transcription
    store/           #   Database layer (PostgreSQL + SQLite, migrations)
  mcp-server/        # Python MCP server (FastMCP, 16 tools)
  dashboard/         # Python dashboard (FastAPI + HTMX)
  embedder/          # Python embedding worker (sentence-transformers + pgvector)
  transcriber/       # Python transcription worker (Whisper + retry logic)
  scripts/           # Service install, migration scripts
  docs/              # Testing guide
  data/              # Runtime: DB, PID files (gitignored)
  logs/              # Runtime logs (gitignored)
  media/             # Downloaded images + audio (gitignored)
  docker-compose.yml # Full stack Docker deployment
  config.toml        # Runtime config (gitignored)
  Makefile           # Native build and run targets
```

## CI/CD

A [GitHub Actions workflow](.github/workflows/nightly-deps.yml) runs daily at 3 AM UTC to update Go and Python dependencies, build, test, and open a PR if anything changed.

## Testing

See [docs/TESTING.md](docs/TESTING.md) for build validation, security audit results, integration test checklist, and known limitations.

## Disclaimer

This project uses unofficial WhatsApp APIs through whatsmeow. It is not endorsed by, affiliated with, or supported by WhatsApp or Meta. Use at your own risk. Using unofficial APIs may violate WhatsApp's Terms of Service and could result in account restrictions.

## Author

[Asim Zeeshan](https://www.linkedin.com/in/asimzeeshan)

## License

[MIT](LICENSE)
