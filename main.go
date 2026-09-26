package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/tarrantro/ns-operator/pkg/controller"
	"github.com/tarrantro/ns-operator/pkg/controller/reconciler"
	ncclient "github.com/tarrantro/ns-operator/pkg/generated/clientset/versioned"
)

func loadKubeConfig(kubePath string) (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = kubePath
	}
	if kubePath == "" {
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		} else {
			return nil, fmt.Errorf("kubeconfig path not provided and home directory not found")
		}
	} else {
		kubeconfig = kubePath
	}

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func main() {
	// try service account first, then fallback to kubeconfig
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = loadKubeConfig("")
		if err != nil {
			fmt.Printf("failed to load kubeconfig: %v\n", err)
			os.Exit(1)
		}
	}

	// Create the Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("failed to create clientset: %v\n", err)
		os.Exit(1)
	}

	ncClientSet, err := ncclient.NewForConfig(config)
	if err != nil {
		fmt.Printf("failed to create namespace class clientset: %v\n", err)
		os.Exit(1)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Printf("failed to create dynamic client: %v\n", err)
		os.Exit(1)
	}

	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(clientset.Discovery()))
	rec := reconciler.New(dynClient, restMapper)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	ctrl := controller.NewController(clientset, ncClientSet, rec)
	if err := ctrl.Run(ctx, 4); err != nil {
		fmt.Printf("controller exited with error: %v\n", err)
		os.Exit(1)
	}
}
