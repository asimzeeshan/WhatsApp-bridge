"""Entry point for the WhatsApp MCP server."""

import logging
import sys


def main():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        stream=sys.stderr,  # stdout reserved for MCP JSON-RPC
    )

    # Import tools to register them with the server
    import whatsapp_mcp.tools  # noqa: F401
    from whatsapp_mcp.server import mcp

    mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
