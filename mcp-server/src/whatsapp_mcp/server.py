"""FastMCP server setup with lifespan management."""

import logging
from contextlib import asynccontextmanager

from fastmcp import FastMCP

from whatsapp_mcp.bridge_client import BridgeClient, BridgeRegistry, build_registry

logger = logging.getLogger(__name__)

registry = build_registry()

# Default bridge client for backward compatibility (used by tools that import `bridge`)
bridge = registry.get()


@asynccontextmanager
async def lifespan(app):
    """Start all bridge clients, health check, yield, then cleanup."""
    await registry.start_all()
    await registry.health_check_all()
    yield {"registry": registry, "bridge": bridge}
    await registry.stop_all()


mcp = FastMCP(
    "whatsapp-mcp",
    lifespan=lifespan,
)
