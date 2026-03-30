# Testing & Validation

## Build Validation

### Go Bridge

```bash
cd bridge
CGO_ENABLED=1 go build -o ../bin/whatsapp-bridge .  # Must compile cleanly
go vet ./...                                          # Must pass with no warnings
go test ./...                                         # All tests must pass
```

**Test coverage:**
- `bridge/indexer/` - Unit tests for URL extraction and platform classification (6 test cases)
- Other packages are integration-tested via live WhatsApp

### Python MCP Server

```bash
cd mcp-server
uv run python -c "from whatsapp_mcp.main import main; print('imports OK')"
```

### Python Dashboard

```bash
cd dashboard
uv run python -c "from whatsapp_dashboard.app import app; print('imports OK')"
```

---

## Security Audit

The codebase has been through a security review. Key findings and their resolution:

| # | Severity | Issue | Resolution |
|---|----------|-------|------------|
| 1 | CRITICAL | SQL injection in telemetry field interpolation | Fixed - whitelist of allowed field names |
| 2 | CRITICAL | Dashboard bound to 0.0.0.0 | Fixed - bound to 127.0.0.1 |
| 3 | HIGH | Path traversal in download handler | Fixed - `filepath.Abs` validation |
| 4 | HIGH | No JID validation on read endpoints | Fixed - `isValidJID` check added |
| 5 | HIGH | Unbounded `io.ReadAll` on uploads | Fixed - `io.LimitReader` with max size |
| 6 | MEDIUM | No authentication on endpoints | Accepted - localhost-only service |
| 7 | MEDIUM | Phone number exposed in `/api/status` | Accepted - localhost-only, needed by dashboard |
| 8 | MEDIUM | Global rate limiter (not per-client) | Accepted - single-client architecture |

---

## Integration Test Checklist

Run these against a live WhatsApp account before deploying:

### Connection
- [ ] Fresh start with no credentials - QR code displayed in terminal
- [ ] Scan QR - bridge logs "connected", `GET /api/status` returns identity
- [ ] Restart with existing credentials - auto-connect, no QR needed
- [ ] Kill bridge with SIGTERM - graceful shutdown, no data loss on restart

### Messaging
- [ ] `send_message` to individual - message appears in WhatsApp
- [ ] `send_group_message` to group - message appears in group
- [ ] `send_group_message` with `quotedMessageId` - reply appears correctly
- [ ] `check_new_messages` with group JID - returns recent messages
- [ ] `get_messages` with group JID - returns message history
- [ ] `get_unread_chats` - shows chats with unread counts
- [ ] `get_unread_messages` - flat list of unread messages

### Contacts & Groups
- [ ] `list_contacts` - returns known contacts
- [ ] `get_contact` with valid JID - returns contact details
- [ ] `list_groups` - returns group list
- [ ] `get_group` with group JID - returns participants and admin roles

### Reactions
- [ ] `send_reaction` with emoji - reaction appears on message
- [ ] `send_reaction` with empty emoji - reaction removed
- [ ] Reaction received from others - stored in reactions table

### Message Editing
- [ ] `edit_message` on own message within 20 minutes - message updated
- [ ] Edit received from others - message content updated in DB, `is_edited` flag set

### Message Revocation
- [ ] `revoke_message` on own message - message deleted for everyone
- [ ] Revoke received from others - `is_revoked` flag set in DB

### Media
- [ ] `send_media` with image - image delivered
- [ ] `download_media` for received image - file saved to disk
- [ ] Image received in watched group - auto-downloaded to `media/images/YYYY-MM-DD/`

### Link Indexing
- [ ] YouTube link shared in group - appears in `GET /api/links?platform=youtube`
- [ ] GitHub link shared - categorized as "github"
- [ ] Multiple links in one message - all indexed

### Telemetry
- [ ] `GET /api/telemetry/daily` - shows today's message/media counters
- [ ] After tool calls - `GET /api/telemetry/tools` shows call history

### Rate Limiting
- [ ] Rapid sends - rate limiter returns 429 after burst exhausted
- [ ] Jitter observed - sends are not instant (0-500ms delay)

### Dashboard
- [ ] `http://localhost:9090` - shows dark-themed dashboard
- [ ] Connection status shows CONNECTED with phone/JID
- [ ] Groups table populated
- [ ] Recent messages auto-refresh every 30s
- [ ] Telemetry counters update
- [ ] Links table shows indexed URLs with platform badges

### Ban Protection
- [ ] Simulate ban scenario - cooldown marker written to `data/cooldown`
- [ ] Restart during cooldown - process exits with code 2
- [ ] After cooldown expires - process starts normally

### Health Endpoint
- [ ] `GET /api/health` - returns memory, disk, DB sizes, goroutine count

### Watermark Persistence
- [ ] Call `check_new_messages` - returns messages
- [ ] Restart bridge - call again - no duplicate messages (watermark persisted in SQLite)

---

## Known Limitations

1. **No unit tests for API handlers** - tested via live WhatsApp integration
2. **No mock WhatsApp server** - tests require a real WhatsApp account
3. **Duplicate utility functions** - `mimeToExt` and `expandTilde` exist in both api/ and client/ packages
4. **Error messages may leak internal paths** - DB errors returned as-is to API callers
5. **Global rate limiter** - single bucket for all callers (acceptable for single-client use)
