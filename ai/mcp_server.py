"""OmniDesk MCP server (stdio).

Environment:
  OMNIDESK_TOKEN    JWT from POST /api/v1/auth/login (required)
  OMNIDESK_API_URL  Go API base URL   (default http://localhost:8080)
  OMNIDESK_AI_URL   AI service URL    (default http://localhost:8000)
"""

import os
import sys

from mcp.server.mcpserver import MCPServer

from omnidesk_ai.mcp_tools import OmniDeskClient

token = os.environ.get("OMNIDESK_TOKEN", "")
if not token:
    sys.exit("OMNIDESK_TOKEN is required")

client = OmniDeskClient(
    api_url=os.environ.get("OMNIDESK_API_URL", "http://localhost:8080"),
    ai_url=os.environ.get("OMNIDESK_AI_URL", "http://localhost:8000"),
    token=token,
)

mcp = MCPServer("omnidesk")


@mcp.tool()
def list_tickets(status: str | None = None) -> list[dict]:
    """List support tickets for the signed-in tenant. Optional status: open, pending, resolved or closed."""
    return client.list_tickets(status)


@mcp.tool()
def get_ticket(ticket_id: int) -> dict:
    """Get one ticket and its status history."""
    return client.get_ticket(ticket_id)


@mcp.tool()
def search_knowledge_base(query: str, top_k: int = 4) -> list[dict]:
    """Search the tenant's knowledge base. Returns passages with similarity scores."""
    return client.search_knowledge_base(query, top_k)


@mcp.tool()
def suggest_reply(question: str) -> dict:
    """Suggest a support reply grounded in the knowledge base. Returns no answer when nothing relevant is found."""
    return client.suggest_reply(question)


if __name__ == "__main__":
    mcp.run()
