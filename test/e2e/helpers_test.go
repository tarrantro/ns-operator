//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	ncv1a1 "github.com/tarrantro/ns-operator/pkg/apis/namespaceclass/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	pollInterval    = 2 * time.Second
	pollTimeout     = 60 * time.Second
	longPollTimeout = 3 * time.Minute
)

// testClients builds the clientsets once per test and fails fast if the
// cluster isn't reachable.
func testClients(t *testing.T) *clients {
	t.Helper()
	c, err := newClients()
	if err != nil {
		t.Fatalf("build kube clients: %v", err)
	}
	return c
}

// uniqueSuffix returns a value safe to append to a resource name so
// parallel/successive test runs don't collide.
func uniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// resourceManifest builds the runtime.RawExtension for an arbitrary
// namespaced resource, suitable for NamespaceClassSpec.Resources. body is
// merged alongside apiVersion/kind/metadata.name, e.g. {"data": {...}} for a
// ConfigMap or {"spec": {...}} for a NetworkPolicy.
func resourceManifest(t *testing.T, apiVersion, kind, name string, body map[string]any) runtime.RawExtension {
	t.Helper()

	manifest := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
	}
	for k, v := range body {
		manifest[k] = v
	}

	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal %s manifest: %v", kind, err)
	}
	return runtime.RawExtension{Raw: raw}
}

func configMapManifest(t *testing.T, name string, data map[string]string) runtime.RawExtension {
	t.Helper()
	return resourceManifest(t, "v1", "ConfigMap", name, map[string]any{"data": data})
}

func serviceAccountManifest(t *testing.T, name string) runtime.RawExtension {
	t.Helper()
	return resourceManifest(t, "v1", "ServiceAccount", name, nil)
}

func secretManifest(t *testing.T, name string, data map[string]string) runtime.RawExtension {
	t.Helper()
	return resourceManifest(t, "v1", "Secret", name, map[string]any{"stringData": data})
}

func networkPolicyManifest(t *testing.T, name string) runtime.RawExtension {
	t.Helper()
	// A NetworkPolicy requires a podSelector; an empty one selects all pods
	// in the namespace, which is enough to exercise apply/prune.
	return resourceManifest(t, "networking.k8s.io/v1", "NetworkPolicy", name, map[string]any{
		"spec": map[string]any{"podSelector": map[string]any{}},
	})
}

// createNamespaceClass creates nc and registers its cleanup.
func createNamespaceClass(t *testing.T, ctx context.Context, c *clients, name string, resources ...runtime.RawExtension) *ncv1a1.NamespaceClass {
	t.Helper()

	nc := &ncv1a1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       ncv1a1.NamespaceClassSpec{Resources: resources},
	}
	created, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Create(ctx, nc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create NamespaceClass %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = c.nc.NamespaceclassV1alpha1().NamespaceClasses().Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return created
}

// createNamespace creates a namespace and registers its cleanup. If
// ncName is non-empty, the namespace is labeled to reference that class.
func createNamespace(t *testing.T, ctx context.Context, c *clients, name, ncName string) *corev1.Namespace {
	t.Helper()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if ncName != "" {
		ns.Labels = map[string]string{ncv1a1.NameLabel: ncName}
	}
	created, err := c.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Namespace %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = c.kube.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return created
}

// waitUntil polls check until it returns true, or fails the test after
// pollTimeout describing what it was waiting for.
func waitUntil(t *testing.T, ctx context.Context, desc string, check func(ctx context.Context) (bool, error)) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(ctx, pollInterval, pollTimeout, true, check); err != nil {
		t.Fatalf("waiting for %s: %v", desc, err)
	}
}

// waitUntilLong is like waitUntil but with a longer timeout, for conditions
// (like GC cascade-deletes after a NamespaceClass is removed) that can take
// longer than the usual reconcile loop.
func waitUntilLong(t *testing.T, ctx context.Context, desc string, check func(ctx context.Context) (bool, error)) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(ctx, pollInterval, longPollTimeout, true, check); err != nil {
		t.Fatalf("waiting for %s: %v", desc, err)
	}
}

func configMapExists(c *clients, ns, name string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		_, err := c.kube.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
		return existsCheck(err)
	}
}

func serviceAccountExists(c *clients, ns, name string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		_, err := c.kube.CoreV1().ServiceAccounts(ns).Get(ctx, name, metav1.GetOptions{})
		return existsCheck(err)
	}
}

func secretExists(c *clients, ns, name string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		_, err := c.kube.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
		return existsCheck(err)
	}
}

func networkPolicyExists(c *clients, ns, name string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		_, err := c.kube.NetworkingV1().NetworkPolicies(ns).Get(ctx, name, metav1.GetOptions{})
		return existsCheck(err)
	}
}

// gone negates an exists check, for waiting on deletion/pruning.
func gone(check func(ctx context.Context) (bool, error)) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		exists, err := check(ctx)
		if err != nil {
			return false, err
		}
		return !exists, nil
	}
}

func existsCheck(err error) (bool, error) {
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func hasObservedKind(nc *ncv1a1.NamespaceClass, apiVersion, kind string) bool {
	for _, ok := range nc.Status.ObservedKinds {
		if ok.APIVersion == apiVersion && ok.Kind == kind {
			return true
		}
	}
	return false
}
