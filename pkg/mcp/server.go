package mcp

import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zxh326/kite/pkg/version"
)

const serverInstructions = `Kite exposes Kubernetes clusters through this MCP server.

Call list_clusters first to discover cluster names. Resource tools accept a cluster name,
a kind (plural resource name such as pods/deployments, or a Kind name), and a namespace
(use _all or omit it to span all namespaces you can access).

All operations are subject to the RBAC roles of the signed-in Kite user. Write tools
(apply_resource, delete_resource, scale_resource, restart_workload) change real cluster
state and are recorded in Kite's resource history. Prefer read tools until you are sure
a change is intended.`

func buildServer(h *Handler) *sdkmcp.Server {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "kite",
		Title:   "Kite Kubernetes Dashboard",
		Version: version.Version,
	}, &sdkmcp.ServerOptions{
		Instructions: serverInstructions,
		SchemaCache:  sdkmcp.NewSchemaCache(),
	})
	registerTools(server, h)
	return server
}
