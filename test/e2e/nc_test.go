//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	ncv1a1 "github.com/tarrantro/ns-operator/pkg/apis/namespaceclass/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestNamespaceClassMultipleKinds covers a NamespaceClass that declares
// several different Kinds (ServiceAccount, ConfigMap, NetworkPolicy) at
// once, then CRUDs the class and checks that both the deployed resources and
// status.observedKinds track the changes:
//   - all three Kinds get applied together;
//   - removing just one Kind from spec.resources prunes only that Kind,
//     leaving the others in place;
//   - status.observedKinds accumulates and never shrinks, even after a Kind
//     is removed from spec.resources.
func TestNamespaceClassMultipleKinds(t *testing.T) {
	c := testClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	suffix := uniqueSuffix()
	ncName := "e2e-nc-multi-" + suffix
	nsName := "e2e-ns-multi-" + suffix
	saName := "e2e-sa-" + suffix
	cmName := "e2e-cm-" + suffix
	npName := "e2e-np-" + suffix

	saManifest := serviceAccountManifest(t, saName)
	cmManifest := configMapManifest(t, cmName, map[string]string{"k": "v"})
	npManifest := networkPolicyManifest(t, npName)

	createNamespaceClass(t, ctx, c, ncName, saManifest, cmManifest, npManifest)
	createNamespace(t, ctx, c, nsName, ncName)

	t.Run("all declared kinds are applied", func(t *testing.T) {
		waitUntil(t, ctx, "ServiceAccount "+nsName+"/"+saName, serviceAccountExists(c, nsName, saName))
		waitUntil(t, ctx, "ConfigMap "+nsName+"/"+cmName, configMapExists(c, nsName, cmName))
		waitUntil(t, ctx, "NetworkPolicy "+nsName+"/"+npName, networkPolicyExists(c, nsName, npName))
	})

	t.Run("status.observedKinds includes all three kinds", func(t *testing.T) {
		waitUntil(t, ctx, "observedKinds to include SA/CM/NetworkPolicy", func(ctx context.Context) (bool, error) {
			got, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Get(ctx, ncName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return hasObservedKind(got, "v1", "ServiceAccount") &&
				hasObservedKind(got, "v1", "ConfigMap") &&
				hasObservedKind(got, "networking.k8s.io/v1", "NetworkPolicy"), nil
		})
	})

	t.Run("removing one kind prunes only that kind", func(t *testing.T) {
		got, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Get(ctx, ncName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get NamespaceClass %s: %v", ncName, err)
		}
		got.Spec.Resources = []runtime.RawExtension{saManifest, cmManifest} // drop the NetworkPolicy
		if _, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Update(ctx, got, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("update NamespaceClass %s to drop NetworkPolicy: %v", ncName, err)
		}

		waitUntil(t, ctx, "NetworkPolicy "+nsName+"/"+npName+" to be pruned", gone(networkPolicyExists(c, nsName, npName)))

		// ServiceAccount and ConfigMap must survive the partial update.
		sa, err := c.kube.CoreV1().ServiceAccounts(nsName).Get(ctx, saName, metav1.GetOptions{})
		if err != nil {
			t.Errorf("ServiceAccount %s/%s should still exist after unrelated update: %v", nsName, saName, err)
		} else if sa == nil {
			t.Errorf("ServiceAccount %s/%s should still exist after unrelated update", nsName, saName)
		}
		if _, err := c.kube.CoreV1().ConfigMaps(nsName).Get(ctx, cmName, metav1.GetOptions{}); err != nil {
			t.Errorf("ConfigMap %s/%s should still exist after unrelated update: %v", nsName, cmName, err)
		}
	})

	t.Run("observedKinds never shrinks", func(t *testing.T) {
		got, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Get(ctx, ncName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get NamespaceClass %s: %v", ncName, err)
		}
		if !hasObservedKind(got, "networking.k8s.io/v1", "NetworkPolicy") {
			t.Errorf("observedKinds dropped NetworkPolicy after it was removed from spec.resources, want it retained")
		}
	})

	t.Run("adding a new kind applies it and updates status", func(t *testing.T) {
		secretName := "e2e-secret-" + suffix
		secretManifestVal := secretManifest(t, secretName, map[string]string{"token": "s3cr3t"})

		got, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Get(ctx, ncName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get NamespaceClass %s: %v", ncName, err)
		}
		// Current spec is [sa, cm] (NetworkPolicy was dropped above); add a
		// brand-new Kind on top of that.
		got.Spec.Resources = []runtime.RawExtension{saManifest, cmManifest, secretManifestVal}
		if _, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Update(ctx, got, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("update NamespaceClass %s to add Secret: %v", ncName, err)
		}

		waitUntil(t, ctx, "Secret "+nsName+"/"+secretName+" to be applied", secretExists(c, nsName, secretName))

		secret, err := c.kube.CoreV1().Secrets(nsName).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get Secret %s/%s: %v", nsName, secretName, err)
		}
		if got := string(secret.Data["token"]); got != "s3cr3t" {
			t.Errorf("Secret data[token] = %q, want %q", got, "s3cr3t")
		}
		if got := secret.Labels[ncv1a1.NameLabel]; got != ncName {
			t.Errorf("Secret label %s = %q, want %q", ncv1a1.NameLabel, got, ncName)
		}

		waitUntil(t, ctx, "observedKinds to include Secret", func(ctx context.Context) (bool, error) {
			got, err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Get(ctx, ncName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return hasObservedKind(got, "v1", "Secret"), nil
		})

		// The previously-applied kinds must still be present alongside the
		// new one.
		if _, err := c.kube.CoreV1().ServiceAccounts(nsName).Get(ctx, saName, metav1.GetOptions{}); err != nil {
			t.Errorf("ServiceAccount %s/%s should still exist after adding Secret: %v", nsName, saName, err)
		}
		if _, err := c.kube.CoreV1().ConfigMaps(nsName).Get(ctx, cmName, metav1.GetOptions{}); err != nil {
			t.Errorf("ConfigMap %s/%s should still exist after adding Secret: %v", nsName, cmName, err)
		}
	})
}

// TestMultipleNamespacesReferenceSameClass checks that one NamespaceClass,
// declaring multiple resource Kinds, referenced by several Namespaces is
// applied independently and in full into each of them.
func TestMultipleNamespacesReferenceSameClass(t *testing.T) {
	c := testClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	suffix := uniqueSuffix()
	ncName := "e2e-nc-shared-" + suffix
	ns1 := "e2e-ns-shared-a-" + suffix
	ns2 := "e2e-ns-shared-b-" + suffix
	cmName := "e2e-cm-" + suffix
	saName := "e2e-sa-" + suffix
	npName := "e2e-np-" + suffix

	createNamespaceClass(t, ctx, c, ncName,
		configMapManifest(t, cmName, map[string]string{"k": "v"}),
		serviceAccountManifest(t, saName),
		networkPolicyManifest(t, npName),
	)
	createNamespace(t, ctx, c, ns1, ncName)
	createNamespace(t, ctx, c, ns2, ncName)

	for _, ns := range []string{ns1, ns2} {
		waitUntil(t, ctx, "ConfigMap "+ns+"/"+cmName, configMapExists(c, ns, cmName))
		waitUntil(t, ctx, "ServiceAccount "+ns+"/"+saName, serviceAccountExists(c, ns, saName))
		waitUntil(t, ctx, "NetworkPolicy "+ns+"/"+npName, networkPolicyExists(c, ns, npName))
	}
}

// TestNamespaceSwitchesNamespaceClass checks that relabeling a Namespace
// from one NamespaceClass to another - where each class declares multiple
// resource Kinds - prunes all of the old class's resources and applies all
// of the new class's resources.
func TestNamespaceSwitchesNamespaceClass(t *testing.T) {
	c := testClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	suffix := uniqueSuffix()
	nc1Name := "e2e-nc-switch-1-" + suffix
	nc2Name := "e2e-nc-switch-2-" + suffix
	nsName := "e2e-ns-switch-" + suffix
	cm1Name := "e2e-cm-switch-1-" + suffix
	sa1Name := "e2e-sa-switch-1-" + suffix
	cm2Name := "e2e-cm-switch-2-" + suffix
	np2Name := "e2e-np-switch-2-" + suffix

	createNamespaceClass(t, ctx, c, nc1Name,
		configMapManifest(t, cm1Name, map[string]string{}),
		serviceAccountManifest(t, sa1Name),
	)
	createNamespaceClass(t, ctx, c, nc2Name,
		configMapManifest(t, cm2Name, map[string]string{}),
		networkPolicyManifest(t, np2Name),
	)
	createNamespace(t, ctx, c, nsName, nc1Name)

	waitUntil(t, ctx, "ConfigMap "+nsName+"/"+cm1Name+" from first class", configMapExists(c, nsName, cm1Name))
	waitUntil(t, ctx, "ServiceAccount "+nsName+"/"+sa1Name+" from first class", serviceAccountExists(c, nsName, sa1Name))

	ns, err := c.kube.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Namespace %s: %v", nsName, err)
	}
	ns.Labels[ncv1a1.NameLabel] = nc2Name
	if _, err := c.kube.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("relabel Namespace %s to second class: %v", nsName, err)
	}

	waitUntil(t, ctx, "ConfigMap "+nsName+"/"+cm2Name+" from second class", configMapExists(c, nsName, cm2Name))
	waitUntil(t, ctx, "NetworkPolicy "+nsName+"/"+np2Name+" from second class", networkPolicyExists(c, nsName, np2Name))
	waitUntil(t, ctx, "ConfigMap "+nsName+"/"+cm1Name+" from first class to be pruned", gone(configMapExists(c, nsName, cm1Name)))
	waitUntil(t, ctx, "ServiceAccount "+nsName+"/"+sa1Name+" from first class to be pruned", gone(serviceAccountExists(c, nsName, sa1Name)))
}

// TestNamespaceClassLabelAddedAndRemoved checks that, for a class declaring
// multiple resource Kinds:
//   - a Namespace created without the class label gets none of them;
//   - adding the label later triggers all of them to be applied;
//   - removing the label later prunes all of them again.
func TestNamespaceClassLabelAddedAndRemoved(t *testing.T) {
	c := testClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	suffix := uniqueSuffix()
	ncName := "e2e-nc-label-" + suffix
	nsName := "e2e-ns-label-" + suffix
	cmName := "e2e-cm-" + suffix
	saName := "e2e-sa-" + suffix

	createNamespaceClass(t, ctx, c, ncName,
		configMapManifest(t, cmName, map[string]string{}),
		serviceAccountManifest(t, saName),
	)
	createNamespace(t, ctx, c, nsName, "") // no label yet

	t.Run("no label means no resources", func(t *testing.T) {
		// Give the controller a few reconcile cycles to prove it stays quiet,
		// then assert neither resource ever showed up.
		time.Sleep(3 * pollInterval)
		if exists, err := configMapExists(c, nsName, cmName)(ctx); err != nil {
			t.Fatalf("check ConfigMap %s/%s: %v", nsName, cmName, err)
		} else if exists {
			t.Errorf("ConfigMap %s/%s should not exist before the namespace references any class", nsName, cmName)
		}
		if exists, err := serviceAccountExists(c, nsName, saName)(ctx); err != nil {
			t.Fatalf("check ServiceAccount %s/%s: %v", nsName, saName, err)
		} else if exists {
			t.Errorf("ServiceAccount %s/%s should not exist before the namespace references any class", nsName, saName)
		}
	})

	t.Run("adding the label applies resources", func(t *testing.T) {
		ns, err := c.kube.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get Namespace %s: %v", nsName, err)
		}
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels[ncv1a1.NameLabel] = ncName
		if _, err := c.kube.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("label Namespace %s: %v", nsName, err)
		}

		waitUntil(t, ctx, "ConfigMap "+nsName+"/"+cmName+" to be applied after labeling", configMapExists(c, nsName, cmName))
		waitUntil(t, ctx, "ServiceAccount "+nsName+"/"+saName+" to be applied after labeling", serviceAccountExists(c, nsName, saName))
	})

	t.Run("removing the label prunes resources", func(t *testing.T) {
		ns, err := c.kube.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get Namespace %s: %v", nsName, err)
		}
		delete(ns.Labels, ncv1a1.NameLabel)
		if _, err := c.kube.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("unlabel Namespace %s: %v", nsName, err)
		}

		waitUntil(t, ctx, "ConfigMap "+nsName+"/"+cmName+" to be pruned after unlabeling", gone(configMapExists(c, nsName, cmName)))
		waitUntil(t, ctx, "ServiceAccount "+nsName+"/"+saName+" to be pruned after unlabeling", gone(serviceAccountExists(c, nsName, saName)))
	})
}

// TestNamespaceClassDeletionRemovesResources checks that deleting a
// NamespaceClass declaring multiple resource Kinds, while it's still
// referenced by a Namespace, results in all of its resources disappearing
// from that Namespace (via the owner reference cascade, since the
// operator's own prune pass is scoped to Kinds declared by NamespaceClasses
// that still exist).
func TestNamespaceClassDeletionRemovesResources(t *testing.T) {
	c := testClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	suffix := uniqueSuffix()
	ncName := "e2e-nc-delete-" + suffix
	nsName := "e2e-ns-delete-" + suffix
	cmName := "e2e-cm-" + suffix
	saName := "e2e-sa-" + suffix
	npName := "e2e-np-" + suffix

	createNamespaceClass(t, ctx, c, ncName,
		configMapManifest(t, cmName, map[string]string{}),
		serviceAccountManifest(t, saName),
		networkPolicyManifest(t, npName),
	)
	createNamespace(t, ctx, c, nsName, ncName)

	waitUntil(t, ctx, "ConfigMap "+nsName+"/"+cmName+" to be applied", configMapExists(c, nsName, cmName))
	waitUntil(t, ctx, "ServiceAccount "+nsName+"/"+saName+" to be applied", serviceAccountExists(c, nsName, saName))
	waitUntil(t, ctx, "NetworkPolicy "+nsName+"/"+npName+" to be applied", networkPolicyExists(c, nsName, npName))

	if err := c.nc.NamespaceclassV1alpha1().NamespaceClasses().Delete(ctx, ncName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete NamespaceClass %s: %v", ncName, err)
	}

	waitUntilLong(t, ctx, "ConfigMap "+nsName+"/"+cmName+" to be removed after class deletion", gone(configMapExists(c, nsName, cmName)))
	waitUntilLong(t, ctx, "ServiceAccount "+nsName+"/"+saName+" to be removed after class deletion", gone(serviceAccountExists(c, nsName, saName)))
	waitUntilLong(t, ctx, "NetworkPolicy "+nsName+"/"+npName+" to be removed after class deletion", gone(networkPolicyExists(c, nsName, npName)))
}
