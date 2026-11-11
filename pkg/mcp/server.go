package mcp

import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zxh326/kite/pkg/version"
)

const serverInstructions = `Kite is a Kubernetes console. This MCP server exposes the Kubernetes clusters registered in Kite, with the same users, RBAC roles, and resource history as the Kite web UI.

When to use these tools:
- Use these tools whenever the user asks about a Kubernetes cluster, namespace, pod, workload, container log, event, or resource status. Prefer them over running kubectl from a shell; kubectl may not be configured for these clusters.
- Also use them for intended changes to cluster state (apply, delete, scale, restart).

Discovery:
- If these tool definitions are not loaded, search for tools with keywords such as "kubernetes", "kite", "pod", "logs", "cluster", "namespace".
- Call list_clusters only when the user names a cluster or you need to disambiguate; otherwise omit cluster to use the Kite default cluster.

Inputs:
- kind accepts a plural resource name (pods, deployments, services, nodes) or a Kind name (Pod, Deployment), including CRD plurals.
- namespace may be omitted or set to _all to span every namespace the user can access. get_resource, describe_resource, and delete_resource require an explicit namespace for namespaced resources.

Results and safety:
- All operations run as the signed-in Kite user and are subject to that user's RBAC rules.
- list_resources returns a compact status block for common resource kinds, similar to kubectl get -o wide.
- Write tools change real cluster state and are recorded in Kite's resource history. Prefer read tools until you are sure a change is intended.`

func buildServer(h *Handler) *sdkmcp.Server {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "kite",
		Title:   "Kite Kubernetes MCP Server",
		Version: version.Version,
	}, &sdkmcp.ServerOptions{
		Instructions: serverInstructions,
		SchemaCache:  sdkmcp.NewSchemaCache(),
	})
	registerTools(server, h)
	return server
}
