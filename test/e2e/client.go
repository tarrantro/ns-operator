//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	ncclient "github.com/tarrantro/ns-operator/pkg/generated/clientset/versioned"
)

// loadKubeConfig builds a *rest.Config from KUBECONFIG, falling back to
// ~/.kube/config, matching the operator's own local-development path in
// main.go. The e2e suite assumes the operator and its CRDs are already
// deployed to the cluster this config points at.
func loadKubeConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home := homedir.HomeDir()
		if home == "" {
			return nil, fmt.Errorf("KUBECONFIG not set and home directory not found")
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build config from %s: %w", kubeconfig, err)
	}
	return config, nil
}

// clients bundles the clientsets the e2e tests need.
type clients struct {
	kube kubernetes.Interface
	nc   ncclient.Interface
}

func newClients() (*clients, error) {
	config, err := loadKubeConfig()
	if err != nil {
		return nil, err
	}

	kube, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kube clientset: %w", err)
	}

	nc, err := ncclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create namespaceclass clientset: %w", err)
	}

	return &clients{kube: kube, nc: nc}, nil
}
