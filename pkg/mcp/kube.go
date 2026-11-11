package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zxh326/kite/pkg/cluster"
	"github.com/zxh326/kite/pkg/common"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// allNamespaces is the namespace value the REST API and RBAC layer use when a
// check applies to all namespaces or to a cluster-scoped resource.
const allNamespaces = "_all"

type resourceRef struct {
	gvr        schema.GroupVersionResource
	gvk        schema.GroupVersionKind
	namespaced bool
}

func (r *resourceRef) resource() string {
	return r.gvr.Resource
}

// resolveResource maps a user supplied kind (plural resource name or Kind, including
// CRDs) to a GVR/GVK.
func resolveResource(cs *cluster.ClientSet, kind string) (*resourceRef, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return nil, fmt.Errorf("kind is required")
	}

	rm := cs.K8sClient.RESTMapper()
	gvr, err := rm.ResourceFor(schema.GroupVersionResource{Resource: strings.ToLower(kind)})
	if err == nil {
		gvk, kindErr := rm.KindFor(gvr)
		if kindErr == nil {
			if mapping, mappingErr := rm.RESTMapping(gvk.GroupKind(), gvk.Version); mappingErr == nil {
				return &resourceRef{
					gvr:        gvr,
					gvk:        gvk,
					namespaced: mapping.Scope.Name() == meta.RESTScopeNameNamespace,
				}, nil
			}
		}
	}

	// Fall back to discovery so singular Kind names ("Deployment") and resources
	// served by aggregated APIs also resolve.
	lists, discoveryErr := cs.K8sClient.ClientSet.Discovery().ServerPreferredResources()
	if len(lists) == 0 {
		if discoveryErr != nil {
			return nil, discoveryErr
		}
		return nil, fmt.Errorf("unknown resource kind %q", kind)
	}
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, resource := range list.APIResources {
			if strings.Contains(resource.Name, "/") {
				continue // skip subresources
			}
			if strings.EqualFold(resource.Name, kind) || strings.EqualFold(resource.Kind, kind) {
				return &resourceRef{
					gvr:        gv.WithResource(resource.Name),
					gvk:        gv.WithKind(resource.Kind),
					namespaced: resource.Namespaced,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("unknown resource kind %q", kind)
}

func dynamicClient(cs *cluster.ClientSet) (dynamic.Interface, error) {
	return dynamic.NewForConfig(cs.K8sClient.Configuration)
}

func (r *resourceRef) rbacNamespace(namespace string) string {
	if !r.namespaced || namespace == "" {
		return allNamespaces
	}
	return namespace
}

func (r *resourceRef) namespaceOrAll(namespace string) string {
	if !r.namespaced {
		return ""
	}
	if namespace == "" {
		return allNamespaces
	}
	return namespace
}

// listNamespace returns the namespace path segment for list calls. The dynamic
// client treats an empty namespace as all namespaces; _all is an RBAC marker,
// not a real namespace.
func (r *resourceRef) listNamespace(namespace string) string {
	if !r.namespaced || namespace == "" || namespace == allNamespaces {
		return ""
	}
	return namespace
}

func (h *Handler) getObject(ctx context.Context, cs *cluster.ClientSet, ref *resourceRef, namespace, name string) (*unstructured.Unstructured, error) {
	dyn, err := dynamicClient(cs)
	if err != nil {
		return nil, err
	}
	obj, err := dyn.Resource(ref.gvr).Namespace(ref.namespaceOrAll(namespace)).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func cleanObject(obj *unstructured.Unstructured) {
	obj.SetManagedFields(nil)
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return
	}
	if _, ok := annotations[common.KubectlAnnotation]; ok {
		delete(annotations, common.KubectlAnnotation)
		obj.SetAnnotations(annotations)
	}
}

func objectYAML(obj *unstructured.Unstructured) string {
	cleanObject(obj)
	data, err := yaml.Marshal(obj.Object)
	if err != nil {
		return ""
	}
	return string(data)
}

type resourceSummary struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	CreatedAt  string            `json:"createdAt,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Status     map[string]any    `json:"status,omitempty"`
}

func summarize(obj *unstructured.Unstructured, ref *resourceRef) resourceSummary {
	return resourceSummary{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		CreatedAt:  obj.GetCreationTimestamp().UTC().Format("2006-01-02T15:04:05Z"),
		Labels:     obj.GetLabels(),
		Status:     resourceStatus(ref, obj),
	}
}

// resourceStatus returns a compact kubectl get -o wide style status block for
// common built-in kinds. pkg/ai has a similar text-only summarizer; this one is
// kept local so the MCP server does not depend on the in-app AI package.
func resourceStatus(ref *resourceRef, obj *unstructured.Unstructured) map[string]any {
	meta := common.LookupResource(ref.gvr.Resource)
	if meta == nil || meta.Group != ref.gvr.Group {
		return nil
	}

	status := map[string]any{}
	switch meta.Plural {
	case common.Pods:
		setNestedString(status, obj.Object, "phase", "status", "phase")
		ready, total, restarts := podContainerStatus(obj.Object)
		status["ready"] = fmt.Sprintf("%d/%d", ready, total)
		status["restarts"] = restarts
		setNestedString(status, obj.Object, "node", "spec", "nodeName")
		setNestedString(status, obj.Object, "podIP", "status", "podIP")
		setNestedString(status, obj.Object, "qosClass", "status", "qosClass")
	case common.Deployments:
		setReplicaInt(status, obj.Object, "replicas", "spec", "replicas")
		setNestedInt(status, obj.Object, "readyReplicas", "status", "readyReplicas")
		setNestedInt(status, obj.Object, "updatedReplicas", "status", "updatedReplicas")
		setNestedInt(status, obj.Object, "availableReplicas", "status", "availableReplicas")
		setNestedInt(status, obj.Object, "unavailableReplicas", "status", "unavailableReplicas")
	case common.StatefulSets:
		setReplicaInt(status, obj.Object, "replicas", "spec", "replicas")
		setNestedInt(status, obj.Object, "readyReplicas", "status", "readyReplicas")
		setNestedInt(status, obj.Object, "currentReplicas", "status", "currentReplicas")
		setNestedInt(status, obj.Object, "updatedReplicas", "status", "updatedReplicas")
	case common.ReplicaSets:
		setReplicaInt(status, obj.Object, "replicas", "spec", "replicas")
		setNestedInt(status, obj.Object, "readyReplicas", "status", "readyReplicas")
		setNestedInt(status, obj.Object, "availableReplicas", "status", "availableReplicas")
	case common.DaemonSets:
		setNestedInt(status, obj.Object, "desiredNumberScheduled", "status", "desiredNumberScheduled")
		setNestedInt(status, obj.Object, "currentNumberScheduled", "status", "currentNumberScheduled")
		setNestedInt(status, obj.Object, "numberReady", "status", "numberReady")
		setNestedInt(status, obj.Object, "numberAvailable", "status", "numberAvailable")
		setNestedInt(status, obj.Object, "numberMisscheduled", "status", "numberMisscheduled")
		setNestedInt(status, obj.Object, "updatedNumberScheduled", "status", "updatedNumberScheduled")
	case common.Nodes:
		if ready := nodeReady(obj.Object); ready != "" {
			status["ready"] = ready
		}
		if roles := nodeRoles(obj.GetLabels()); len(roles) > 0 {
			status["roles"] = roles
		}
		setNestedString(status, obj.Object, "kubeletVersion", "status", "nodeInfo", "kubeletVersion")
		if ip := nodeInternalIP(obj.Object); ip != "" {
			status["internalIP"] = ip
		}
	case common.Services:
		setNestedString(status, obj.Object, "type", "spec", "type")
		setNestedString(status, obj.Object, "clusterIP", "spec", "clusterIP")
		if ports := servicePorts(obj.Object); len(ports) > 0 {
			status["ports"] = ports
		}
	case common.Jobs:
		setNestedInt(status, obj.Object, "active", "status", "active")
		setNestedInt(status, obj.Object, "succeeded", "status", "succeeded")
		setNestedInt(status, obj.Object, "failed", "status", "failed")
	case common.Ingresses:
		setNestedString(status, obj.Object, "class", "spec", "ingressClassName")
		if hosts := ingressHosts(obj.Object); len(hosts) > 0 {
			status["hosts"] = hosts
		}
		if addresses := loadBalancerAddresses(obj.Object); len(addresses) > 0 {
			status["loadBalancer"] = addresses
		}
	case common.PersistentVolumeClaims:
		setNestedString(status, obj.Object, "phase", "status", "phase")
		setNestedString(status, obj.Object, "capacity", "status", "capacity", "storage")
	case common.CronJobs:
		setNestedString(status, obj.Object, "schedule", "spec", "schedule")
		if suspend, ok, _ := unstructured.NestedBool(obj.Object, "spec", "suspend"); ok {
			status["suspend"] = suspend
		}
		setNestedString(status, obj.Object, "lastScheduleTime", "status", "lastScheduleTime")
	case common.Namespaces:
		setNestedString(status, obj.Object, "phase", "status", "phase")
	}

	if len(status) == 0 {
		return nil
	}
	return status
}

func podContainerStatus(obj map[string]interface{}) (ready, total, restarts int64) {
	if containers, ok, _ := unstructured.NestedSlice(obj, "spec", "containers"); ok {
		total = int64(len(containers))
	}
	statuses, ok, _ := unstructured.NestedSlice(obj, "status", "containerStatuses")
	if !ok {
		return ready, total, restarts
	}
	for _, item := range statuses {
		container, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if isReady, _ := container["ready"].(bool); isReady {
			ready++
		}
		if count, ok := container["restartCount"].(int64); ok {
			restarts += count
		}
	}
	return ready, total, restarts
}

func setReplicaInt(dst map[string]any, obj map[string]interface{}, key string, fields ...string) {
	replicas := int64(1)
	if v, ok, _ := unstructured.NestedInt64(obj, fields...); ok {
		replicas = v
	}
	dst[key] = replicas
}

func setNestedInt(dst map[string]any, obj map[string]interface{}, key string, fields ...string) {
	if v, ok, _ := unstructured.NestedInt64(obj, fields...); ok {
		dst[key] = v
	}
}

func setNestedString(dst map[string]any, obj map[string]interface{}, key string, fields ...string) {
	if v, ok, _ := unstructured.NestedString(obj, fields...); ok && v != "" {
		dst[key] = v
	}
}

func nodeReady(obj map[string]interface{}) string {
	conditions, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	for _, item := range conditions {
		condition, ok := item.(map[string]interface{})
		if !ok || condition["type"] != "Ready" {
			continue
		}
		if ready, ok := condition["status"].(string); ok {
			return ready
		}
	}
	return ""
}

func nodeRoles(labels map[string]string) []string {
	roles := make([]string, 0, 3)
	for key := range labels {
		if !strings.HasPrefix(key, "node-role.kubernetes.io/") {
			continue
		}
		role := strings.TrimPrefix(key, "node-role.kubernetes.io/")
		if role == "" {
			role = "worker"
		}
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func nodeInternalIP(obj map[string]interface{}) string {
	addresses, _, _ := unstructured.NestedSlice(obj, "status", "addresses")
	for _, item := range addresses {
		address, ok := item.(map[string]interface{})
		if !ok || address["type"] != "InternalIP" {
			continue
		}
		if ip, ok := address["address"].(string); ok {
			return ip
		}
	}
	return ""
}

func servicePorts(obj map[string]interface{}) []string {
	ports, _, _ := unstructured.NestedSlice(obj, "spec", "ports")
	result := make([]string, 0, len(ports))
	for _, item := range ports {
		port, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		number, ok := port["port"].(int64)
		if !ok {
			continue
		}
		protocol, _ := port["protocol"].(string)
		if protocol == "" {
			protocol = "TCP"
		}
		result = append(result, fmt.Sprintf("%d/%s", number, protocol))
	}
	sort.Strings(result)
	return result
}

func ingressHosts(obj map[string]interface{}) []string {
	rules, _, _ := unstructured.NestedSlice(obj, "spec", "rules")
	seen := make(map[string]struct{}, len(rules))
	hosts := make([]string, 0, len(rules))
	for _, item := range rules {
		rule, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		host, ok := rule["host"].(string)
		if !ok || host == "" {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func loadBalancerAddresses(obj map[string]interface{}) []string {
	ingress, _, _ := unstructured.NestedSlice(obj, "status", "loadBalancer", "ingress")
	addresses := make([]string, 0, len(ingress))
	for _, item := range ingress {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if ip, ok := entry["ip"].(string); ok && ip != "" {
			addresses = append(addresses, ip)
			continue
		}
		if hostname, ok := entry["hostname"].(string); ok && hostname != "" {
			addresses = append(addresses, hostname)
		}
	}
	sort.Strings(addresses)
	return addresses
}
