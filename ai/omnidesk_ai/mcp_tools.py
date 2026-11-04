"""Tool implementations behind the MCP server.

They call the Go API and the AI service over HTTP with the caller's JWT, so the
MCP server has exactly the permissions of that user and tenant, and nothing more.
All tools are read-only.
"""

from dataclasses import dataclass

import httpx

VALID_STATUSES = {"open", "pending", "resolved", "closed"}


@dataclass
class OmniDeskClient:
    api_url: str
    ai_url: str
    token: str
    http: httpx.Client | None = None

    def _client(self) -> httpx.Client:
        return self.http or httpx.Client(timeout=20)

    def _request(self, method: str, url: str, **kw):
        client = self._client()
        try:
            resp = client.request(method, url, headers={"Authorization": f"Bearer {self.token}"}, **kw)
        finally:
            if self.http is None:
                client.close()
        if resp.status_code == 404:
            raise ValueError("not found")
        if resp.status_code in (401, 403):
            raise PermissionError("token rejected")
        resp.raise_for_status()
        return resp.json()

    def list_tickets(self, status: str | None = None) -> list[dict]:
        if status is not None and status not in VALID_STATUSES:
            raise ValueError(f"status must be one of {sorted(VALID_STATUSES)}")
        params = {"status": status} if status else None
        return self._request("GET", f"{self.api_url}/api/v1/tickets", params=params)

    def get_ticket(self, ticket_id: int) -> dict:
        if ticket_id < 1:
            raise ValueError("ticket_id must be positive")
        return self._request("GET", f"{self.api_url}/api/v1/tickets/{ticket_id}")

    def search_knowledge_base(self, query: str, top_k: int = 4) -> list[dict]:
        data = self._request(
            "POST", f"{self.ai_url}/kb/search", json={"query": query, "top_k": min(max(top_k, 1), 10)}
        )
        return data["passages"]

    def suggest_reply(self, question: str) -> dict:
        return self._request("POST", f"{self.ai_url}/suggest", json={"question": question})
