package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zxh326/kite/pkg/common"
	"github.com/zxh326/kite/pkg/model"
	"github.com/zxh326/kite/pkg/rbac"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	serializeryaml "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/describe"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultListLimit = 100
	maxListLimit     = 500
	maxLogBytes      = 512 * 1024

	restartedAtAnnotation = "kite.kubernetes.io/restartedAt"
)

type listClustersInput struct{}

type namespaceListInput struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
}

type resourceListInput struct {
	Cluster       string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Kind          string `json:"kind" jsonschema:"resource kind or plural name, for example pods, deployment, or a CRD plural"`
	Namespace     string `json:"namespace,omitempty" jsonschema:"namespace; omit or use _all for all namespaces you can access"`
	LabelSelector string `json:"label_selector,omitempty" jsonschema:"Kubernetes label selector, for example app=web"`
	Limit         int    `json:"limit,omitempty" jsonschema:"maximum number of items to return (default 100, max 500)"`
}

type resourceGetInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Kind      string `json:"kind" jsonschema:"resource kind or plural name, for example pods or deployment"`
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace; required for namespaced resources"`
	Name      string `json:"name" jsonschema:"resource name"`
}

type describeInput struct {
	Cluster    string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Kind       string `json:"kind" jsonschema:"resource kind or plural name, for example pods or deployment"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"namespace; required for namespaced resources"`
	Name       string `json:"name" jsonschema:"resource name"`
	ShowEvents bool   `json:"show_events,omitempty" jsonschema:"include recent events in the output (default false)"`
}

type podLogsInput struct {
	Cluster      string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Namespace    string `json:"namespace" jsonschema:"pod namespace"`
	Pod          string `json:"pod" jsonschema:"pod name"`
	Container    string `json:"container,omitempty" jsonschema:"container name; omit for the pod's default container"`
	Previous     bool   `json:"previous,omitempty" jsonschema:"return logs from the previous container instance"`
	TailLines    int    `json:"tail_lines,omitempty" jsonschema:"number of log lines from the end (default 200, max 5000)"`
	SinceSeconds int    `json:"since_seconds,omitempty" jsonschema:"only return logs newer than this many seconds"`
}

type eventsInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace; omit or use _all for all namespaces you can access"`
	Kind      string `json:"kind,omitempty" jsonschema:"only events for this involved resource kind"`
	Name      string `json:"name,omitempty" jsonschema:"only events for this involved resource name"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum number of events to return (default 100, max 500)"`
}

type applyInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	YAML      string `json:"yaml" jsonschema:"Kubernetes resource manifest in YAML"`
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace to use when the manifest does not set one"`
}

type deleteInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Kind      string `json:"kind" jsonschema:"resource kind or plural name"`
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace; required for namespaced resources"`
	Name      string `json:"name" jsonschema:"resource name"`
}

type scaleInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Kind      string `json:"kind" jsonschema:"deployment, statefulset, or replicaset"`
	Namespace string `json:"namespace" jsonschema:"namespace"`
	Name      string `json:"name" jsonschema:"resource name"`
	Replicas  int32  `json:"replicas" jsonschema:"desired replica count (0 pauses the workload)"`
}

type restartInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"cluster name; omit to use the default cluster"`
	Kind      string `json:"kind" jsonschema:"deployment, statefulset, or daemonset"`
	Namespace string `json:"namespace" jsonschema:"namespace"`
	Name      string `json:"name" jsonschema:"resource name"`
}

func registerTools(s *sdkmcp.Server, h *Handler) {
	destructive := true
	nonDestructive := false
	readOnly := &sdkmcp.ToolAnnotations{ReadOnlyHint: true}

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_clusters",
		Description: "List Kubernetes clusters the signed-in user can access. Call this before other cluster-scoped tools.",
		Annotations: readOnly,
	}, h.listClusters)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_namespaces",
		Description: "List namespaces in a cluster that the signed-in user can access.",
		Annotations: readOnly,
	}, h.listNamespaces)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_resources",
		Description: "List Kubernetes resources of a kind in a cluster, optionally filtered by namespace and label selector. Returns name, namespace, kind, creation time, and labels.",
		Annotations: readOnly,
	}, h.listResources)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_resource",
		Description: "Get a single Kubernetes resource as JSON.",
		Annotations: readOnly,
	}, h.getResource)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "describe_resource",
		Description: "Describe a Kubernetes resource using kubectl-style output. Falls back to YAML for custom resources without a describer.",
		Annotations: readOnly,
	}, h.describeResource)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_pod_logs",
		Description: "Get recent logs from a pod container. Requires the pod 'log' permission in RBAC.",
		Annotations: readOnly,
	}, h.getPodLogs)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_events",
		Description: "List Kubernetes events, optionally filtered to one involved resource.",
		Annotations: readOnly,
	}, h.listEvents)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "apply_resource",
		Description: "Create or update a Kubernetes resource from a YAML manifest. Requires create or update permission and is recorded in resource history.",
		Annotations: &sdkmcp.ToolAnnotations{DestructiveHint: &nonDestructive, IdempotentHint: true},
	}, h.applyResource)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "delete_resource",
		Description: "Delete a Kubernetes resource. Requires delete permission and is recorded in resource history.",
		Annotations: &sdkmcp.ToolAnnotations{DestructiveHint: &destructive},
	}, h.deleteResource)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "scale_resource",
		Description: "Scale a Deployment, StatefulSet, or ReplicaSet. Requires update permission.",
		Annotations: &sdkmcp.ToolAnnotations{DestructiveHint: &nonDestructive, IdempotentHint: true},
	}, h.scaleResource)
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "restart_workload",
		Description: "Restart a Deployment, StatefulSet, or DaemonSet by updating its pod template annotation. Requires update permission and is recorded in resource history.",
		Annotations: &sdkmcp.ToolAnnotations{DestructiveHint: &nonDestructive},
	}, h.restartWorkload)
}

func jsonResult(v any) (*sdkmcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(data))
}

func textResult(text string) (*sdkmcp.CallToolResult, any, error) {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
	}, nil, nil
}

func (h *Handler) listClusters(_ context.Context, req *sdkmcp.CallToolRequest, _ listClustersInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(h.cm.GetAccessibleClusters(user))
}

func (h *Handler) listNamespaces(ctx context.Context, req *sdkmcp.CallToolRequest, in namespaceListInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	list, err := cs.K8sClient.ClientSet.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	type namespaceInfo struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		CreatedAt string `json:"createdAt,omitempty"`
	}
	namespaces := make([]namespaceInfo, 0, len(list.Items))
	for i := range list.Items {
		namespace := &list.Items[i]
		if !rbac.CanAccessNamespace(user, cs.Name, namespace.Name) {
			continue
		}
		namespaces = append(namespaces, namespaceInfo{
			Name:      namespace.Name,
			Status:    string(namespace.Status.Phase),
			CreatedAt: namespace.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(namespaces, func(i, j int) bool { return namespaces[i].Name < namespaces[j].Name })
	return jsonResult(map[string]any{"cluster": cs.Name, "count": len(namespaces), "namespaces": namespaces})
}

func (h *Handler) listResources(ctx context.Context, req *sdkmcp.CallToolRequest, in resourceListInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	ref, err := resolveResource(cs, in.Kind)
	if err != nil {
		return nil, nil, err
	}

	namespace := in.Namespace
	if ref.namespaced && namespace != "" && namespace != allNamespaces {
		if !rbac.CanAccess(user, ref.resource(), string(common.VerbGet), cs.Name, namespace) {
			return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbGet), ref.resource(), namespace, cs.Name))
		}
	} else if !rbac.CanAccess(user, ref.resource(), string(common.VerbGet), cs.Name, allNamespaces) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbGet), ref.resource(), allNamespaces, cs.Name))
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	dyn, err := dynamicClient(cs)
	if err != nil {
		return nil, nil, err
	}
	list, err := dyn.Resource(ref.gvr).Namespace(ref.namespaceOrAll(namespace)).List(ctx, metav1.ListOptions{
		LabelSelector: in.LabelSelector,
		Limit:         int64(limit),
	})
	if err != nil {
		return nil, nil, err
	}

	items := make([]resourceSummary, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		if namespace := item.GetNamespace(); namespace != "" && !rbac.CanAccessNamespace(user, cs.Name, namespace) {
			continue
		}
		items = append(items, summarize(item))
	}
	return jsonResult(map[string]any{
		"cluster":   cs.Name,
		"kind":      ref.gvk.Kind,
		"namespace": namespace,
		"count":     len(items),
		"items":     items,
	})
}

func (h *Handler) getResource(ctx context.Context, req *sdkmcp.CallToolRequest, in resourceGetInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	ref, err := resolveResource(cs, in.Kind)
	if err != nil {
		return nil, nil, err
	}
	if ref.namespaced && (in.Namespace == "" || in.Namespace == allNamespaces) {
		return nil, nil, fmt.Errorf("namespace is required for %s", ref.resource())
	}
	if !rbac.CanAccess(user, ref.resource(), string(common.VerbGet), cs.Name, ref.rbacNamespace(in.Namespace)) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbGet), ref.resource(), ref.rbacNamespace(in.Namespace), cs.Name))
	}
	obj, err := h.getObject(ctx, cs, ref, in.Namespace, in.Name)
	if err != nil {
		return nil, nil, err
	}
	cleanObject(obj)
	return jsonResult(obj.Object)
}

func (h *Handler) describeResource(ctx context.Context, req *sdkmcp.CallToolRequest, in describeInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	ref, err := resolveResource(cs, in.Kind)
	if err != nil {
		return nil, nil, err
	}
	if ref.namespaced && (in.Namespace == "" || in.Namespace == allNamespaces) {
		return nil, nil, fmt.Errorf("namespace is required for %s", ref.resource())
	}
	if !rbac.CanAccess(user, ref.resource(), string(common.VerbGet), cs.Name, ref.rbacNamespace(in.Namespace)) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbGet), ref.resource(), ref.rbacNamespace(in.Namespace), cs.Name))
	}

	if describer, ok := describe.DescriberFor(ref.gvk.GroupKind(), cs.K8sClient.Configuration); ok {
		output, err := describer.Describe(in.Namespace, in.Name, describe.DescriberSettings{ShowEvents: in.ShowEvents})
		if err == nil {
			return textResult(output)
		}
	}

	obj, err := h.getObject(ctx, cs, ref, in.Namespace, in.Name)
	if err != nil {
		return nil, nil, err
	}
	return textResult(objectYAML(obj))
}

func (h *Handler) getPodLogs(ctx context.Context, req *sdkmcp.CallToolRequest, in podLogsInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	if in.Namespace == "" || in.Pod == "" {
		return nil, nil, fmt.Errorf("namespace and pod are required")
	}
	if !rbac.CanAccess(user, "pods", string(common.VerbLog), cs.Name, in.Namespace) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbLog), "pods", in.Namespace, cs.Name))
	}

	tail := int64(200)
	if in.TailLines > 0 {
		tail = int64(in.TailLines)
	}
	if tail > 5000 {
		tail = 5000
	}
	options := &corev1.PodLogOptions{
		Container: in.Container,
		Previous:  in.Previous,
		TailLines: &tail,
	}
	if in.SinceSeconds > 0 {
		since := int64(in.SinceSeconds)
		options.SinceSeconds = &since
	}

	data, err := cs.K8sClient.ClientSet.CoreV1().Pods(in.Namespace).GetLogs(in.Pod, options).DoRaw(ctx)
	if err != nil {
		return nil, nil, err
	}
	text := string(data)
	if len(text) > maxLogBytes {
		text = text[:maxLogBytes] + "\n... (logs truncated)"
	}
	return textResult(text)
}

func (h *Handler) listEvents(ctx context.Context, req *sdkmcp.CallToolRequest, in eventsInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}

	namespace := in.Namespace
	rbacNS := allNamespaces
	if namespace != "" && namespace != allNamespaces {
		rbacNS = namespace
	}
	if !rbac.CanAccess(user, "events", string(common.VerbGet), cs.Name, rbacNS) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbGet), "events", rbacNS, cs.Name))
	}

	selector := ""
	if in.Kind != "" {
		ref, err := resolveResource(cs, in.Kind)
		if err != nil {
			return nil, nil, err
		}
		selector = "involvedObject.kind=" + ref.gvk.Kind
	}
	if in.Name != "" {
		if selector != "" {
			selector += ","
		}
		selector += "involvedObject.name=" + in.Name
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	listNamespace := ""
	if rbacNS != allNamespaces {
		listNamespace = rbacNS
	}
	list, err := cs.K8sClient.ClientSet.CoreV1().Events(listNamespace).List(ctx, metav1.ListOptions{
		FieldSelector: selector,
		Limit:         int64(limit),
	})
	if err != nil {
		return nil, nil, err
	}

	type eventSummary struct {
		Type      string `json:"type"`
		Reason    string `json:"reason"`
		Message   string `json:"message"`
		Count     int32  `json:"count"`
		Component string `json:"component,omitempty"`
		Object    string `json:"object"`
		Namespace string `json:"namespace,omitempty"`
		FirstSeen string `json:"firstSeen,omitempty"`
		LastSeen  string `json:"lastSeen,omitempty"`
	}
	events := make([]eventSummary, 0, len(list.Items))
	for i := range list.Items {
		event := &list.Items[i]
		if event.Namespace != "" && !rbac.CanAccessNamespace(user, cs.Name, event.Namespace) {
			continue
		}
		firstSeen := event.FirstTimestamp.UTC().Format(time.RFC3339)
		if firstSeen == "0001-01-01T00:00:00Z" {
			firstSeen = event.EventTime.UTC().Format(time.RFC3339)
		}
		lastSeen := event.LastTimestamp.UTC().Format(time.RFC3339)
		if lastSeen == "0001-01-01T00:00:00Z" {
			lastSeen = event.EventTime.UTC().Format(time.RFC3339)
		}
		events = append(events, eventSummary{
			Type:      event.Type,
			Reason:    event.Reason,
			Message:   event.Message,
			Count:     event.Count,
			Component: event.Source.Component,
			Object:    event.InvolvedObject.Kind + "/" + event.InvolvedObject.Name,
			Namespace: event.Namespace,
			FirstSeen: firstSeen,
			LastSeen:  lastSeen,
		})
	}
	return jsonResult(map[string]any{"cluster": cs.Name, "count": len(events), "events": events})
}

func (h *Handler) applyResource(ctx context.Context, req *sdkmcp.CallToolRequest, in applyInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}

	decoder := serializeryaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	if _, _, err := decoder.Decode([]byte(in.YAML), nil, obj); err != nil {
		return nil, nil, fmt.Errorf("invalid YAML: %w", err)
	}
	if obj.GetKind() == "" || obj.GetAPIVersion() == "" {
		return nil, nil, fmt.Errorf("manifest must set apiVersion and kind")
	}
	if obj.GetName() == "" {
		return nil, nil, fmt.Errorf("manifest must set metadata.name")
	}
	if in.Namespace != "" && obj.GetNamespace() == "" {
		obj.SetNamespace(in.Namespace)
	}

	gvk := obj.GroupVersionKind()
	mapping, err := cs.K8sClient.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, nil, err
	}
	namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
	if namespaced && obj.GetNamespace() == "" {
		return nil, nil, fmt.Errorf("namespace is required for %s", gvk.Kind)
	}
	ref := &resourceRef{gvr: mapping.Resource, gvk: gvk, namespaced: namespaced}
	rbacNS := ref.rbacNamespace(obj.GetNamespace())

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(gvk)
	getErr := cs.K8sClient.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}, existing)

	verb := string(common.VerbCreate)
	switch {
	case getErr == nil:
		verb = string(common.VerbUpdate)
	case !apierrors.IsNotFound(getErr):
		return nil, nil, getErr
	}
	if !rbac.CanAccess(user, ref.resource(), verb, cs.Name, rbacNS) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), verb, ref.resource(), rbacNS, cs.Name))
	}

	var previousYAML string
	if getErr == nil {
		previousYAML = objectYAML(existing)
	}

	var applyErr error
	if verb == string(common.VerbCreate) {
		applyErr = cs.K8sClient.Create(ctx, obj)
	} else {
		obj.SetResourceVersion(existing.GetResourceVersion())
		applyErr = cs.K8sClient.Update(ctx, obj)
	}
	recordHistory(user, cs.Name, ref.resource(), obj.GetName(), obj.GetNamespace(), "apply", in.YAML, previousYAML, applyErr)
	if applyErr != nil {
		return nil, nil, applyErr
	}

	return jsonResult(map[string]any{
		"cluster":   cs.Name,
		"operation": verb,
		"kind":      obj.GetKind(),
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
	})
}

func (h *Handler) deleteResource(ctx context.Context, req *sdkmcp.CallToolRequest, in deleteInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	ref, err := resolveResource(cs, in.Kind)
	if err != nil {
		return nil, nil, err
	}
	if ref.namespaced && (in.Namespace == "" || in.Namespace == allNamespaces) {
		return nil, nil, fmt.Errorf("namespace is required for %s", ref.resource())
	}
	rbacNS := ref.rbacNamespace(in.Namespace)
	if !rbac.CanAccess(user, ref.resource(), string(common.VerbDelete), cs.Name, rbacNS) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbDelete), ref.resource(), rbacNS, cs.Name))
	}

	obj, err := h.getObject(ctx, cs, ref, in.Namespace, in.Name)
	if err != nil {
		return nil, nil, err
	}
	previousYAML := objectYAML(obj)

	dyn, err := dynamicClient(cs)
	if err != nil {
		return nil, nil, err
	}
	deleteErr := dyn.Resource(ref.gvr).Namespace(ref.namespaceOrAll(in.Namespace)).Delete(ctx, in.Name, metav1.DeleteOptions{})
	recordHistory(user, cs.Name, ref.resource(), in.Name, in.Namespace, "delete", "", previousYAML, deleteErr)
	if deleteErr != nil {
		return nil, nil, deleteErr
	}
	return jsonResult(map[string]any{"cluster": cs.Name, "deleted": ref.gvk.Kind + "/" + in.Name, "namespace": in.Namespace})
}

func (h *Handler) scaleResource(ctx context.Context, req *sdkmcp.CallToolRequest, in scaleInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	ref, err := resolveResource(cs, in.Kind)
	if err != nil {
		return nil, nil, err
	}
	if ref.gvr.Group != "apps" || (ref.gvr.Resource != "deployments" && ref.gvr.Resource != "statefulsets" && ref.gvr.Resource != "replicasets") {
		return nil, nil, fmt.Errorf("scale_resource supports deployments, statefulsets, and replicasets")
	}
	if in.Namespace == "" || in.Namespace == allNamespaces {
		return nil, nil, fmt.Errorf("namespace is required")
	}
	if !rbac.CanAccess(user, ref.resource(), string(common.VerbUpdate), cs.Name, in.Namespace) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbUpdate), ref.resource(), in.Namespace, cs.Name))
	}

	apps := cs.K8sClient.ClientSet.AppsV1()
	switch ref.gvr.Resource {
	case "deployments":
		scale, err := apps.Deployments(in.Namespace).GetScale(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, nil, err
		}
		scale.Spec.Replicas = in.Replicas
		if _, err := apps.Deployments(in.Namespace).UpdateScale(ctx, in.Name, scale, metav1.UpdateOptions{}); err != nil {
			return nil, nil, err
		}
	case "statefulsets":
		scale, err := apps.StatefulSets(in.Namespace).GetScale(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, nil, err
		}
		scale.Spec.Replicas = in.Replicas
		if _, err := apps.StatefulSets(in.Namespace).UpdateScale(ctx, in.Name, scale, metav1.UpdateOptions{}); err != nil {
			return nil, nil, err
		}
	case "replicasets":
		scale, err := apps.ReplicaSets(in.Namespace).GetScale(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, nil, err
		}
		scale.Spec.Replicas = in.Replicas
		if _, err := apps.ReplicaSets(in.Namespace).UpdateScale(ctx, in.Name, scale, metav1.UpdateOptions{}); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("scale_resource supports deployments, statefulsets, and replicasets")
	}
	return jsonResult(map[string]any{
		"cluster":   cs.Name,
		"scaled":    ref.gvk.Kind + "/" + in.Name,
		"namespace": in.Namespace,
		"replicas":  in.Replicas,
	})
}

func (h *Handler) restartWorkload(ctx context.Context, req *sdkmcp.CallToolRequest, in restartInput) (*sdkmcp.CallToolResult, any, error) {
	user, err := userFromRequest(req)
	if err != nil {
		return nil, nil, err
	}
	cs, err := h.cm.GetClientSet(in.Cluster)
	if err != nil {
		return nil, nil, err
	}
	ref, err := resolveResource(cs, in.Kind)
	if err != nil {
		return nil, nil, err
	}
	if ref.gvr.Group != "apps" || (ref.gvr.Resource != "deployments" && ref.gvr.Resource != "statefulsets" && ref.gvr.Resource != "daemonsets") {
		return nil, nil, fmt.Errorf("restart_workload supports deployments, statefulsets, and daemonsets")
	}
	if in.Namespace == "" || in.Namespace == allNamespaces {
		return nil, nil, fmt.Errorf("namespace is required")
	}
	if !rbac.CanAccess(user, ref.resource(), string(common.VerbUpdate), cs.Name, in.Namespace) {
		return nil, nil, errors.New(rbac.NoAccess(user.Key(), string(common.VerbUpdate), ref.resource(), in.Namespace, cs.Name))
	}

	dyn, err := dynamicClient(cs)
	if err != nil {
		return nil, nil, err
	}
	obj, err := dyn.Resource(ref.gvr).Namespace(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	previousYAML := objectYAML(obj)

	annotations, _, err := unstructured.NestedStringMap(obj.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return nil, nil, err
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[restartedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
	if err := unstructured.SetNestedStringMap(obj.Object, annotations, "spec", "template", "metadata", "annotations"); err != nil {
		return nil, nil, err
	}

	updated, updateErr := dyn.Resource(ref.gvr).Namespace(in.Namespace).Update(ctx, obj, metav1.UpdateOptions{})
	var currentYAML string
	if updateErr == nil {
		currentYAML = objectYAML(updated)
	}
	recordHistory(user, cs.Name, ref.resource(), in.Name, in.Namespace, "update", currentYAML, previousYAML, updateErr)
	if updateErr != nil {
		return nil, nil, updateErr
	}
	return jsonResult(map[string]any{
		"cluster":   cs.Name,
		"restarted": ref.gvk.Kind + "/" + in.Name,
		"namespace": in.Namespace,
	})
}

func recordHistory(user model.User, clusterName, resource, name, namespace, operation, currentYAML, previousYAML string, opErr error) {
	history := model.ResourceHistory{
		ClusterName:   clusterName,
		ResourceType:  resource,
		ResourceName:  name,
		Namespace:     namespace,
		OperationType: operation,
		ResourceYAML:  currentYAML,
		PreviousYAML:  previousYAML,
		Success:       opErr == nil,
		OperatorID:    user.ID,
	}
	if opErr != nil {
		history.ErrorMessage = opErr.Error()
	}
	if err := model.DB.Create(&history).Error; err != nil {
		klog.Errorf("Failed to record MCP resource history: %v", err)
	}
}
