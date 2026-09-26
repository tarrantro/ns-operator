package controller

import (
	"context"
	"encoding/json"
	"fmt"

	ncv1a1 "github.com/tarrantro/ns-operator/pkg/apis/namespaceclass/v1alpha1"
	"github.com/tarrantro/ns-operator/pkg/controller/reconciler"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
)

// reconcile applies every resource in nc.Spec.Resources into ns
func (c *Controller) reconcile(ctx context.Context, nc *ncv1a1.NamespaceClass, ns *corev1.Namespace) (map[schema.GroupVersionKind]sets.Set[string], error) {
	desired := map[schema.GroupVersionKind]sets.Set[string]{}
	objs := make([]*unstructured.Unstructured, 0, len(nc.Spec.Resources))

	for _, resource := range nc.Spec.Resources {
		u := &unstructured.Unstructured{}
		if err := u.UnmarshalJSON(resource.Raw); err != nil {
			return nil, fmt.Errorf("invalid resource manifest: %w", err)
		}
		objs = append(objs, u)

		gvk := u.GroupVersionKind()
		if desired[gvk] == nil {
			desired[gvk] = sets.New[string]()
		}
		desired[gvk].Insert(u.GetName())
	}

	// Persist any newly-seen Kinds to Status.ObservedKinds,
	// so the recorded set never lags behind what's
	// actually in the cluster
	if err := c.recordObservedKinds(ctx, nc, desired); err != nil {
		return nil, fmt.Errorf("record observed kinds: %w", err)
	}

	owner := metav1.OwnerReference{
		APIVersion: ncv1a1.SchemeGroupVersion.String(),
		Kind:       "NamespaceClass",
		Name:       nc.Name,
		UID:        nc.UID,
		Controller: ptr.To(true),
	}

	var errs []error
	for _, u := range objs {
		if err := c.reconciler.Apply(ctx, ns.Name, owner, u); err != nil {
			err = fmt.Errorf("apply %s %q: %w", u.GroupVersionKind(), u.GetName(), err)
			c.recorder.Eventf(ns, corev1.EventTypeWarning, "ApplyFailed", "NamespaceClass %s: %v", nc.Name, err)
			errs = append(errs, err)
			continue
		}
	}

	return desired, utilerrors.NewAggregate(errs)
}

// recordObservedKinds appends any GVKs in desired that aren't already in
// nc.Status.ObservedKinds, via a Server-Side Apply patch to the status.
func (c *Controller) recordObservedKinds(ctx context.Context, nc *ncv1a1.NamespaceClass, desired map[schema.GroupVersionKind]sets.Set[string]) error {
	observed := observedKindSet(nc.Status.ObservedKinds)
	var newKinds []ncv1a1.ObservedKind
	for gvk := range desired {
		if observed.Has(gvk) {
			continue
		}
		apiVersion, kind := gvk.ToAPIVersionAndKind()
		newKinds = append(newKinds, ncv1a1.ObservedKind{APIVersion: apiVersion, Kind: kind})
	}
	if len(newKinds) == 0 {
		return nil
	}

	patch := &ncv1a1.NamespaceClass{
		TypeMeta:   metav1.TypeMeta{APIVersion: ncv1a1.SchemeGroupVersion.String(), Kind: "NamespaceClass"},
		ObjectMeta: metav1.ObjectMeta{Name: nc.Name},
		Status:     ncv1a1.NamespaceClassStatus{ObservedKinds: newKinds},
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	_, err = c.ncClientSet.NamespaceclassV1alpha1().NamespaceClasses().Patch(
		ctx, nc.Name, types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: reconciler.FieldManager, Force: ptr.To(true)},
		"status",
	)
	return err
}

func observedKindSet(observed []ncv1a1.ObservedKind) sets.Set[schema.GroupVersionKind] {
	set := sets.New[schema.GroupVersionKind]()
	for _, o := range observed {
		set.Insert(schema.FromAPIVersionAndKind(o.APIVersion, o.Kind))
	}
	return set
}
