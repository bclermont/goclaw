# MCP Server Setup Notes

Findings verified through hands-on investigation of goclaw's MCP integration.

## MCP Server Setup Findings

Key facts verified through investigation:

1. **Transport type**: goclaw uses `"streamable-http"` (NOT `"http"` — that's Claude Code's format). DB column `transport` must be exactly `"streamable-http"`.

2. **Auth headers**: goclaw reads the `headers` jsonb column from `mcp_servers` table and passes them via `transport.WithHTTPHeaders()`. If `headers` is empty/null → 401 Unauthorized on `initialize`.

3. **Protocol**: Streamable HTTP MCP requires `initialize` handshake first (gets `Mcp-Session-Id`), then `tools/list`. The `mcp-go` library handles this automatically — session ID is managed internally.

4. **Tool grants**: Grants are in `mcp_agent_grants` table (NOT `mcp_grants`). Other grant tables: `mcp_user_grants`, `mcp_context_grants`.

5. **Master vs tenant tokens**: `/v1/mcp/servers` is tenant-scoped. Master gateway token returns empty list. Need tenant API key for API calls.

6. **Discovery errors appear in docker logs**: `mcp.discover_tools server=X error="initialize: transport error: unauthorized (401)"` — check with `docker logs goclaw 2>&1 | grep "mcp.discover"`

7. **Tools endpoint**: `GET /v1/mcp/servers/{id}/tools` — if empty `{"tools":[]}`, check: (a) transport type correct, (b) headers populated, (c) server actually reachable.
