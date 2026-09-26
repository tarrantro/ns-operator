package reconciler

import (
	"context"
	"fmt"

	ncv1a1 "github.com/tarrantro/ns-operator/pkg/apis/namespaceclass/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
)

const FieldManager = "ns-operator"

const ManagedByLabel = ncv1a1.NameLabel

// Reconciler applies and prunes arbitrary-Kind resources on behalf of a NamespaceClass.
type Reconciler struct {
	dynClient  dynamic.Interface
	restMapper meta.RESTMapper
}

// New builds a Reconciler backed by dynClient, mapping GVKs to GVRs via restMapper.
func New(dynClient dynamic.Interface, restMapper meta.RESTMapper) *Reconciler {
	return &Reconciler{dynClient: dynClient, restMapper: restMapper}
}

// Apply sets namespace/owner/label on obj and applies it via Server-Side
// Apply, so repeated calls are idempotent.
func (r *Reconciler) Apply(ctx context.Context, namespace string, owner metav1.OwnerReference, obj *unstructured.Unstructured) error {
	gvr, err := r.gvrFor(obj.GroupVersionKind())
	if err != nil {
		return err
	}

	obj.SetNamespace(namespace)
	obj.SetOwnerReferences([]metav1.OwnerReference{owner})
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[ManagedByLabel] = owner.Name
	obj.SetLabels(labels)

	data, err := obj.MarshalJSON()
	if err != nil {
		return err
	}

	_, err = r.dynClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, obj.GetName(), types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: FieldManager, Force: ptr.To(true)},
	)
	return err
}

// Prune deletes every resource in namespace, of any Kind in checkGVKs, that
// is labelled as managed by this controller but is not present in desired.
func (r *Reconciler) Prune(ctx context.Context, namespace string, desired map[schema.GroupVersionKind]sets.Set[string], checkGVKs []schema.GroupVersionKind) error {
	requirement, err := labels.NewRequirement(ManagedByLabel, selection.Exists, nil)
	if err != nil {
		return err
	}
	selector := labels.NewSelector().Add(*requirement)

	for _, gvk := range checkGVKs {
		gvr, err := r.gvrFor(gvk)
		if err != nil {
			if meta.IsNoMatchError(err) {
				// Kind no longer registered in this cluster; nothing to prune.
				continue
			}
			return err
		}

		list, err := r.dynClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
		if err != nil {
			return fmt.Errorf("list %s in namespace %s: %w", gvk, namespace, err)
		}

		for _, item := range list.Items {
			if desired[gvk].Has(item.GetName()) {
				continue
			}
			if err := r.dynClient.Resource(gvr).Namespace(namespace).Delete(ctx, item.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("prune %s %q in namespace %s: %w", gvk, item.GetName(), namespace, err)
			}
		}
	}
	return nil
}

// gvrFor maps a GVK to the GVR
func (r *Reconciler) gvrFor(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	mapping, err := r.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		return schema.GroupVersionResource{}, fmt.Errorf("resource kind %s is not namespace-scoped", gvk)
	}
	return mapping.Resource, nil
}
