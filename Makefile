.PHONY: build build-bridge install-mcp install-dashboard install-embedder install-transcriber \
       run-bridge run-mcp run-dashboard run-embedder run-transcriber run-all stop-all status \
       logs-bridge logs-dashboard test lint clean \
       service-install service-uninstall service-restart service-status service-logs

build: build-bridge install-mcp install-dashboard install-embedder

# --- Go Bridge ---

build-bridge:
	cd bridge && CGO_ENABLED=1 go build -o ../bin/whatsapp-bridge .

run-bridge:
	./bin/whatsapp-bridge --config config.toml

# --- Python MCP Server (uv manages .venv inside mcp-server/) ---

install-mcp:
	cd mcp-server && uv pip install -e .

run-mcp:
	cd mcp-server && uv run whatsapp-mcp

# --- Python Dashboard (uv manages .venv inside dashboard/) ---

install-dashboard:
	cd dashboard && uv pip install -e .

run-dashboard:
	cd dashboard && uv run uvicorn whatsapp_dashboard.app:app --host 127.0.0.1 --port 9090

# --- Run All (both backgrounded, no terminal needed) ---

run-all: build-bridge
	@mkdir -p data logs
	@# Stop any existing processes first
	@if [ -f data/bridge.pid ] && kill -0 $$(cat data/bridge.pid) 2>/dev/null; then \
		echo "Bridge already running (PID: $$(cat data/bridge.pid)). Use 'make stop-all' first."; \
		exit 1; \
	fi
	@if [ -f data/dashboard.pid ] && kill -0 $$(cat data/dashboard.pid) 2>/dev/null; then \
		echo "Dashboard already running (PID: $$(cat data/dashboard.pid)). Use 'make stop-all' first."; \
		exit 1; \
	fi
	@echo "Starting bridge in background..."
	@nohup ./bin/whatsapp-bridge --config config.toml > logs/bridge.log 2>&1 & echo $$! > data/bridge.pid
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if curl -sf http://127.0.0.1:8080/api/status > /dev/null 2>&1; then \
			echo "  Bridge healthy (PID: $$(cat data/bridge.pid))"; \
			break; \
		fi; \
		if [ $$i -eq 10 ]; then echo "  Warning: bridge not yet healthy"; fi; \
		sleep 2; \
	done
	@echo "Starting dashboard in background..."
	@nohup sh -c 'cd dashboard && uv run uvicorn whatsapp_dashboard.app:app --host 127.0.0.1 --port 9090' > logs/dashboard.log 2>&1 & echo $$! > data/dashboard.pid
	@sleep 1
	@echo ""
	@echo "All services started:"
	@echo "  Bridge:    http://127.0.0.1:8080  (PID: $$(cat data/bridge.pid), log: logs/bridge.log)"
	@echo "  Dashboard: http://127.0.0.1:9090  (PID: $$(cat data/dashboard.pid), log: logs/dashboard.log)"
	@echo ""
	@echo "Use 'make stop-all' to stop both."
	@echo "Use 'make status' to check status."
	@echo "Use 'make logs-bridge' or 'make logs-dashboard' to tail logs."

stop-all:
	@if [ -f data/dashboard.pid ]; then \
		kill $$(cat data/dashboard.pid) 2>/dev/null && echo "Dashboard stopped." || echo "Dashboard not running."; \
		rm -f data/dashboard.pid; \
	fi
	@if [ -f data/bridge.pid ]; then \
		kill $$(cat data/bridge.pid) 2>/dev/null && echo "Bridge stopped." || echo "Bridge not running."; \
		rm -f data/bridge.pid; \
	fi

status:
	@echo "Bridge:"
	@if [ -f data/bridge.pid ] && kill -0 $$(cat data/bridge.pid) 2>/dev/null; then \
		echo "  Running (PID: $$(cat data/bridge.pid))"; \
		curl -sf http://127.0.0.1:8080/api/status | python3 -m json.tool 2>/dev/null || echo "  API not responding"; \
	else \
		echo "  Not running"; \
	fi
	@echo ""
	@echo "Dashboard:"
	@if [ -f data/dashboard.pid ] && kill -0 $$(cat data/dashboard.pid) 2>/dev/null; then \
		echo "  Running (PID: $$(cat data/dashboard.pid)) — http://127.0.0.1:9090"; \
	else \
		echo "  Not running"; \
	fi

logs-bridge:
	@tail -f logs/bridge.log

logs-dashboard:
	@tail -f logs/dashboard.log

# --- Embedder (vector search) ---

install-embedder:
	cd embedder && uv pip install -e .

run-embedder:
	cd embedder && uv run whatsapp-embedder

# --- Transcriber (voice note transcription) ---

install-transcriber:
	cd transcriber && uv pip install -e .

run-transcriber:
	cd transcriber && uv run whatsapp-transcriber

# --- Whisper (transcription) ---

whisper-start:
	docker compose up -d whisper
	@echo "Whisper starting on http://127.0.0.1:8443"

whisper-stop:
	docker compose down

whisper-logs:
	docker compose logs -f whisper

# --- Testing ---

test: test-bridge
test-bridge:
	cd bridge && go test ./...

lint:
	cd bridge && go vet ./...

# --- macOS Service (launchd) ---

service-install:
	@./scripts/install-service.sh install

service-uninstall:
	@./scripts/install-service.sh uninstall

service-restart:
	@./scripts/install-service.sh restart

service-status:
	@./scripts/install-service.sh status

service-logs:
	@./scripts/install-service.sh logs

clean:
	rm -rf bin/ data/*.db
