package mcp

import (
	"context"
	"fmt"
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
}

func summarize(obj *unstructured.Unstructured) resourceSummary {
	return resourceSummary{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		CreatedAt:  obj.GetCreationTimestamp().UTC().Format("2006-01-02T15:04:05Z"),
		Labels:     obj.GetLabels(),
	}
}
