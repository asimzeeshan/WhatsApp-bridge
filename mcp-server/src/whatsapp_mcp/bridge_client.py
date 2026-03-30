"""HTTP client for communicating with the Go WhatsApp bridge."""

import asyncio
import json
import logging
import os

import httpx
from tenacity import retry, stop_after_attempt, wait_exponential, retry_if_exception_type

logger = logging.getLogger(__name__)

BRIDGE_URL = os.environ.get("BRIDGE_URL", "http://127.0.0.1:8080")
BRIDGE_ACCOUNTS = os.environ.get("BRIDGE_ACCOUNTS", "")


class BridgeClient:
    """Async HTTP client for the Go bridge REST API."""

    def __init__(self, base_url: str = BRIDGE_URL, name: str = "default", read_only: bool = False):
        self.base_url = base_url.rstrip("/")
        self.name = name
        self.read_only = read_only
        self._client: httpx.AsyncClient | None = None

    async def start(self):
        self._client = httpx.AsyncClient(
            base_url=self.base_url,
            timeout=30.0,
        )

    async def stop(self):
        if self._client:
            await self._client.aclose()
            self._client = None

    @property
    def client(self) -> httpx.AsyncClient:
        if self._client is None:
            raise RuntimeError("BridgeClient not started")
        return self._client

    async def health_check(self, attempts: int = 5, delay: float = 2.0) -> bool:
        """Poll GET /api/status until the bridge is reachable."""
        for i in range(attempts):
            try:
                resp = await self.client.get("/api/status")
                if resp.status_code == 200:
                    data = resp.json()
                    logger.info("bridge '%s' connected: state=%s", self.name, data.get("state"))
                    return True
            except httpx.ConnectError:
                pass
            if i < attempts - 1:
                logger.info("waiting for bridge '%s' (attempt %d/%d)...", self.name, i + 1, attempts)
                await asyncio.sleep(delay)
        logger.error("bridge '%s' not reachable after %d attempts", self.name, attempts)
        return False

    # -- GET with retry --

    @retry(
        stop=stop_after_attempt(3),
        wait=wait_exponential(multiplier=1, min=1, max=10),
        retry=retry_if_exception_type((httpx.ConnectError, httpx.ReadTimeout)),
        reraise=True,
    )
    async def get(self, path: str, params: dict | None = None) -> dict:
        resp = await self.client.get(path, params=params)
        resp.raise_for_status()
        return resp.json()

    # -- POST (no retry to prevent duplicates) --

    async def post(self, path: str, json: dict | None = None) -> dict:
        resp = await self.client.post(path, json=json)
        resp.raise_for_status()
        return resp.json()

    async def post_multipart(self, path: str, data: dict, files: dict) -> dict:
        resp = await self.client.post(path, data=data, files=files)
        resp.raise_for_status()
        return resp.json()

    # -- Telemetry helper --

    async def record_tool_call(self, tool_name: str, duration_ms: int, success: bool, error_msg: str = ""):
        """Fire-and-forget telemetry recording."""
        try:
            await self.post("/api/telemetry/tool", json={
                "tool_name": tool_name,
                "duration_ms": duration_ms,
                "success": success,
                "error_msg": error_msg,
            })
        except Exception:
            pass  # Non-blocking, never fail on telemetry


class BridgeRegistry:
    """Registry of multiple bridge clients for multi-account support."""

    def __init__(self):
        self.clients: dict[str, BridgeClient] = {}
        self.default_name: str = "default"

    def add(self, client: BridgeClient):
        self.clients[client.name] = client
        # First non-read-only client becomes the default for send operations
        if not client.read_only and self.default_name == "default" and "default" not in self.clients:
            self.default_name = client.name

    def get(self, name: str | None = None) -> BridgeClient:
        """Get a bridge client by account name. Returns default if name is None."""
        if name is None:
            # Return default (first registered client)
            return next(iter(self.clients.values()))
        if name not in self.clients:
            raise ValueError(f"Unknown account: '{name}'. Available: {list(self.clients.keys())}")
        return self.clients[name]

    def get_for_send(self, name: str | None = None) -> BridgeClient:
        """Get a bridge client for send operations. Raises if account is read-only."""
        client = self.get(name)
        if client.read_only:
            raise ValueError(f"Account '{client.name}' is read-only. Cannot send messages.")
        return client

    @property
    def account_names(self) -> list[str]:
        return list(self.clients.keys())

    async def start_all(self):
        for client in self.clients.values():
            await client.start()

    async def health_check_all(self):
        for client in self.clients.values():
            await client.health_check()

    async def stop_all(self):
        for client in self.clients.values():
            await client.stop()


def build_registry() -> BridgeRegistry:
    """Build a BridgeRegistry from environment variables.

    Supports two modes:
    1. Single account (backward compatible): BRIDGE_URL env var
    2. Multi-account: BRIDGE_ACCOUNTS env var as JSON array
       Example: [{"name":"eva","url":"http://127.0.0.1:8080"},{"name":"personal","url":"http://127.0.0.1:8081","read_only":true}]
    """
    registry = BridgeRegistry()

    if BRIDGE_ACCOUNTS:
        try:
            accounts = json.loads(BRIDGE_ACCOUNTS)
            for acc in accounts:
                client = BridgeClient(
                    base_url=acc["url"],
                    name=acc["name"],
                    read_only=acc.get("read_only", False),
                )
                registry.add(client)
                logger.info("registered account '%s' at %s (read_only=%s)", acc["name"], acc["url"], acc.get("read_only", False))
        except (json.JSONDecodeError, KeyError) as e:
            logger.error("failed to parse BRIDGE_ACCOUNTS: %s", e)
            raise

    if not registry.clients:
        # Fallback to single account from BRIDGE_URL
        registry.add(BridgeClient(base_url=BRIDGE_URL, name="default", read_only=False))

    return registry
