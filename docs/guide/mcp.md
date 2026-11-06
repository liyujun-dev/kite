# MCP Server

Kite exposes a remote [Model Context Protocol](https://modelcontextprotocol.io) server at `/mcp`.
AI clients such as Claude Code, Cursor, and MCP Inspector can use it to inspect and operate Kubernetes
clusters through Kite with the same users, RBAC roles, and resource history as the web UI.

## Enable the endpoint

The MCP endpoint is enabled by default. Disable it with `MCP_ENABLED=false`, or with
`mcpEnabled: false` in the Helm chart. When `KITE_BASE` is set, the endpoints live under that base path.

## Connect a client

### Claude Code

```bash
claude mcp add --transport http kite https://kite.example.com/mcp
```

### MCP Inspector

```bash
npx @modelcontextprotocol/inspector --url https://kite.example.com/mcp
```

### Manual configuration

```json
{
  "mcpServers": {
    "kite": {
      "type": "http",
      "url": "https://kite.example.com/mcp"
    }
  }
}
```

On the first connection the client discovers the authorization server automatically:

1. `POST /mcp` without a token returns `401` with
   `WWW-Authenticate: Bearer resource_metadata="..."`.
2. The client fetches the protected-resource metadata and authorization-server metadata.
3. The client registers itself through dynamic client registration (`POST /api/oauth/register`) and
   opens the authorization URL in a browser.
4. Sign in to Kite and approve the consent screen.
5. The client exchanges the authorization code (PKCE `S256`) for an access token and a refresh token.

## API key authentication

Clients that cannot run the browser flow can use an existing Kite API key instead:

```bash
curl https://kite.example.com/mcp \
  -H "Authorization: Bearer kite<id>-<key>" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}'
```

Create API keys in Kite under **Settings → API Keys**. API keys never expire; delete them to revoke
access.

## Permissions

Every tool runs as the authenticated Kite user and applies the same RBAC checks as the REST API.

| Tool                | RBAC verb                 | Resource                               |
| ------------------- | ------------------------- | -------------------------------------- |
| `list_clusters`     | cluster access            | —                                      |
| `list_namespaces`   | namespace access          | namespaces                             |
| `list_resources`    | `get`                     | the requested resource                 |
| `get_resource`      | `get`                     | the requested resource                 |
| `describe_resource` | `get`                     | the requested resource                 |
| `get_pod_logs`      | `log`                     | pods                                   |
| `list_events`       | `get`                     | events                                 |
| `apply_resource`    | `create` or `update`      | the resource in the manifest            |
| `delete_resource`   | `delete`                  | the requested resource                 |
| `scale_resource`    | `update`                  | deployments/statefulsets/replicasets   |
| `restart_workload`  | `update`                  | deployments/statefulsets/daemonsets    |

Cluster-scoped resources and all-namespace checks use the namespace value `_all`, exactly like the
REST API. Results from `_all` listings are filtered to the namespaces the user can access.

Write tools are recorded in Kite's **Resource History**.

## Configuration

| Variable       | Description                                                                 |
| -------------- | --------------------------------------------------------------------------- |
| `MCP_ENABLED`  | Enable the MCP and OAuth endpoints. Default: `true`.                        |
| `MCP_ISSUER`   | Issuer URL advertised to clients. Defaults to `HOST` + base path.           |
| `MCP_RESOURCE` | MCP resource URL. Defaults to `<issuer>/mcp`.                           |
| `HOST`         | External URL of Kite, for example `https://kite.example.com`.               |

Behind a reverse proxy, either set `HOST` or forward `X-Forwarded-Proto` and `X-Forwarded-Host`, so
that discovery metadata, authorization redirects, and token audiences use the public URL.

## Security notes

- Dynamic client registration is intentionally open so MCP clients can connect, but a token is only
  issued after a Kite login and an explicit consent approval.
- Access tokens expire after one hour. Refresh tokens expire after 30 days and are rotated on every
  use; replaying an old refresh token revokes the whole token family.
- Disabling a Kite user takes effect on the next MCP request.
- The user's RBAC rules still apply. A role that can read `secrets` allows MCP clients to read them
  too, so scope roles carefully.
