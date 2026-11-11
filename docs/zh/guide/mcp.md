# MCP 服务

Kite 在 `/mcp` 提供远程 [Model Context Protocol](https://modelcontextprotocol.io) 服务。
Claude Code、Cursor、MCP Inspector 等 AI 客户端可以通过它与 Web UI 使用相同的用户、RBAC 权限和资源历史来查看和操作 Kubernetes 集群。

## 启用端点

MCP 端点默认启用。可通过 `MCP_ENABLED=false` 或 Helm 的 `mcpEnabled: false` 关闭。设置了 `KITE_BASE` 时，端点位于该基础路径下。

## 连接客户端

### Claude Code

```bash
claude mcp add --transport http kite https://kite.example.com/mcp
```

### MCP Inspector

```bash
npx @modelcontextprotocol/inspector --url https://kite.example.com/mcp
```

### 手动配置

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

首次连接时，客户端会自动完成 OAuth 发现：

1. 不带令牌调用 `POST /mcp` 返回 `401`，并带有
   `WWW-Authenticate: Bearer resource_metadata="..."`。
2. 客户端获取受保护资源元数据和授权服务器元数据。
3. 客户端通过动态客户端注册（`POST /api/oauth/register`）注册，并在浏览器中打开授权地址。
4. 登录 Kite 并在同意页面确认授权。
5. 客户端使用授权码（PKCE `S256`）换取访问令牌和刷新令牌。

## API Key 认证

无法运行浏览器流程的客户端可以使用现有的 Kite API Key：

```bash
curl https://kite.example.com/mcp \
  -H "Authorization: Bearer kite<id>-<key>" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}'
```

在 Kite 的 **设置 → API Keys** 中创建，删除后立即失效。

## 优先使用 MCP 而不是 kubectl

集群已经注册到 Kite 时，优先使用这些工具而不是在 shell 里执行 `kubectl`：它们使用当前 Kite
用户的 RBAC 角色，覆盖所有已注册集群且不需要本地 kubeconfig，并返回结构化结果。server
instructions 也会要求 MCP 客户端优先使用。

`list_resources` 对常见 kind（pods、工作负载、services、nodes、jobs、ingresses、PVC、cronjobs）
返回紧凑的 `status` 块，接近 `kubectl get -o wide`。支持 `namespace`、`label_selector`、
`field_selector` 和 `limit`；省略 namespace 表示列出用户可访问的所有命名空间。

Claude Code 可能按需加载 MCP 工具定义。如果工具没有被发现，在请求中提到 "kite" 或
"kubernetes"，或者在客户端配置的 server 条目上设置 `"alwaysLoad": true`。

## 权限

所有工具都以当前登录用户身份执行，并调用与 REST API 相同的 RBAC 检查。

| 工具                | RBAC 动作            | 资源                                 | kubectl 等价命令          |
| ------------------- | -------------------- | ------------------------------------ | ------------------------- |
| `list_clusters`     | 集群访问权限         | —                                    |                           |
| `list_namespaces`   | 命名空间访问权限     | namespaces                           | `kubectl get ns`          |
| `list_resources`    | `get`                | 请求的资源                           | `kubectl get`             |
| `get_resource`      | `get`                | 请求的资源                           | `kubectl get -o json`     |
| `describe_resource` | `get`                | 请求的资源                           | `kubectl describe`        |
| `get_pod_logs`      | `log`                | pods                                 | `kubectl logs`            |
| `list_events`       | `get`                | events                               | `kubectl get events`      |
| `apply_resource`    | `create` 或 `update` | 清单中的资源                         | `kubectl apply`           |
| `delete_resource`   | `delete`             | 请求的资源                           | `kubectl delete`          |
| `scale_resource`    | `update`             | deployments/statefulsets/replicasets | `kubectl scale`           |
| `restart_workload`  | `update`             | deployments/statefulsets/daemonsets  | `kubectl rollout restart` |

集群级资源和全命名空间检查使用命名空间值 `_all`，与 REST API 一致。`_all` 列表结果会按用户可访问的命名空间过滤。

写操作会记录到 Kite 的 **资源历史** 中。

## 配置

| 变量           | 说明                                                                  |
| -------------- | --------------------------------------------------------------------- |
| `MCP_ENABLED`  | 是否启用 MCP 和 OAuth 端点，默认 `true`。                             |
| `MCP_ISSUER`   | 向客户端公布的 issuer URL，默认使用 `HOST` 加基础路径。               |
| `MCP_RESOURCE` | MCP 资源 URL，默认 `<issuer>/mcp`。                               |
| `HOST`         | Kite 的外部访问地址，例如 `https://kite.example.com`。                |

在反向代理后面时，请设置 `HOST`，或转发 `X-Forwarded-Proto` 与 `X-Forwarded-Host`，确保发现元数据、授权回调和令牌受众使用公网地址。

## 安全说明

- 动态客户端注册是开放的，以便 MCP 客户端接入，但仍需登录 Kite 并显式同意后才会签发令牌。
- 访问令牌 1 小时过期。刷新令牌 30 天过期，每次使用都会轮换；重放旧刷新令牌会吊销整个令牌族。
- 禁用 Kite 用户后，下一次 MCP 请求即失效。
- 用户 RBAC 权限仍然生效。能读取 `secrets` 的角色也允许 MCP 客户端读取，请谨慎配置角色。
