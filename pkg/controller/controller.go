package controller

import (
	"context"
	"fmt"
	"time"

	ncv1a1 "github.com/tarrantro/ns-operator/pkg/apis/namespaceclass/v1alpha1"
	"github.com/tarrantro/ns-operator/pkg/controller/reconciler"
	ncclient "github.com/tarrantro/ns-operator/pkg/generated/clientset/versioned"
	ncinformers "github.com/tarrantro/ns-operator/pkg/generated/informers/externalversions"
	ncv1a1informers "github.com/tarrantro/ns-operator/pkg/generated/informers/externalversions/namespaceclass/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

// Controller reconciles Namespaces against the NamespaceClass they reference
type Controller struct {
	ncClientSet ncclient.Interface

	nsInformer coreinformers.NamespaceInformer
	ncInformer ncv1a1informers.TypedNamespaceClassInformer

	// reconciler applies/prunes the arbitrary-Kind resources a NamespaceClass
	// declares, using the dynamic client.
	reconciler *reconciler.Reconciler

	queue workqueue.TypedRateLimitingInterface[string]

	recorder   record.EventRecorder
	maxRetries int
}

// NewController creates a new Controller with rate-limited work queue
func NewController(clientset kubernetes.Interface, ncClientSet ncclient.Interface, rec *reconciler.Reconciler) *Controller {
	// Namespace informer, cluster-scoped, resync every 30s
	nsFactory := informers.NewSharedInformerFactory(clientset, time.Second*30)
	nsInformer := nsFactory.Core().V1().Namespaces()

	// NamespaceClass informer, also cluster-scoped
	ncFactory := ncinformers.NewSharedInformerFactory(ncClientSet, time.Second*30)
	ncInformer := ncFactory.Namespaceclass().V1alpha1().NamespaceClasses()

	// Create a rate-limiting work queue
	// Items are rate limited with exponential backoff
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](
			time.Millisecond*5, // Base delay
			time.Second*30,     // Max delay
		),
	)

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "ns-operator"})

	controller := &Controller{
		ncClientSet: ncClientSet,
		nsInformer:  nsInformer,
		ncInformer:  ncInformer,
		reconciler:  rec,
		queue:       queue,
		recorder:    recorder,
		maxRetries:  5,
	}

	// Register event handlers for Namespace events
	nsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleNamespace,
		UpdateFunc: func(old, new interface{}) {
			newNs := new.(*corev1.Namespace)
			oldNs := old.(*corev1.Namespace)
			if newNs.ResourceVersion == oldNs.ResourceVersion {
				return
			}
			controller.handleNamespace(new)
		},
		DeleteFunc: controller.handleNamespace,
	})

	// Register event handlers for NamespaceClass events: any change should
	// re-reconcile every Namespace that references it.
	ncInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleNamespaceClass,
		UpdateFunc: func(old, new interface{}) {
			controller.handleNamespaceClass(new)
		},
		DeleteFunc: controller.handleNamespaceClass,
	})

	return controller
}

// handleNamespace enqueues the Namespace
func (c *Controller) handleNamespace(obj interface{}) {
	object, ok := c.asMetaObject(obj)
	if !ok {
		return
	}
	c.queue.Add(object.GetName())
}

// handleNamespaceClass enqueues every Namespace Class
func (c *Controller) handleNamespaceClass(obj interface{}) {
	object, ok := c.asMetaObject(obj)
	if !ok {
		return
	}

	namespaces, err := c.nsInformer.Lister().List(labels.Everything())
	if err != nil {
		fmt.Printf("failed to list namespaces: %v\n", err)
		return
	}

	ncName := object.GetName()
	for _, ns := range namespaces {
		if ns.Labels[ncv1a1.NameLabel] == ncName {
			c.queue.Add(ns.Name)
		}
	}
}

func (c *Controller) asMetaObject(obj interface{}) (metav1.Object, bool) {
	object, ok := obj.(metav1.Object)
	if ok {
		return object, true
	}

	tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
	if !ok {
		fmt.Printf("error decoding object, invalid type: %T\n", obj)
		return nil, false
	}

	object, ok = tombstone.Obj.(metav1.Object)
	if !ok {
		fmt.Printf("error decoding object tombstone, invalid type: %T\n", tombstone.Obj)
		return nil, false
	}
	return object, true
}

// Run starts the controller with the specified number of workers
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	fmt.Println("starting controller")

	stopCh := ctx.Done()
	go c.nsInformer.Informer().Run(stopCh)
	go c.ncInformer.Informer().Run(stopCh)

	if !cache.WaitForCacheSync(stopCh, c.nsInformer.Informer().HasSynced, c.ncInformer.Informer().HasSynced) {
		return fmt.Errorf("failed to sync caches")
	}

	// Start worker goroutines
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	<-ctx.Done()
	fmt.Println("shutting down controller")
	return nil
}

// runWorker processes items from the queue
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextItem(ctx) {
	}
}

// processNextItem handles a single work item
func (c *Controller) processNextItem(ctx context.Context) bool {
	// Get next item from queue (blocks if empty)
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	// Process the item
	err := c.syncHandler(ctx, key)
	if err == nil {
		// Success - clear retry count and remove from queue
		c.queue.Forget(key)
		return true
	}

	// Handle retry logic
	if c.queue.NumRequeues(key) < c.maxRetries {
		fmt.Printf("error processing %s (will retry): %v\n", key, err)
		c.queue.AddRateLimited(key)
		return true
	}

	// Max retries exceeded
	fmt.Printf("dropping %s after %d retries: %v\n", key, c.maxRetries, err)
	c.queue.Forget(key)

	return true
}

// syncHandler synchronizes the state of the namespace to match the
// NamespaceClass it references
func (c *Controller) syncHandler(ctx context.Context, name string) error {
	ns, err := c.nsInformer.Lister().Get(name)
	if apierrors.IsNotFound(err) {
		// Namespace was deleted; Kubernetes garbage collects everything in it.
		fmt.Printf("namespace %s was deleted\n", name)
		return nil
	}
	if err != nil {
		return err
	}

	desired := map[schema.GroupVersionKind]sets.Set[string]{}

	if ncName, ok := ns.Labels[ncv1a1.NameLabel]; ok {
		nc, err := c.ncInformer.Lister().Get(ncName)
		switch {
		case apierrors.IsNotFound(err):
			// Referenced NamespaceClass doesn't exist, don't reconcile, but still prune any previously managed resources.
			fmt.Printf("namespace %s references missing NamespaceClass %s\n", ns.Name, ncName)
		case err != nil:
			return fmt.Errorf("get NamespaceClass %s for namespace %s: %w", ncName, ns.Name, err)
		default:
			fmt.Printf("reconciling namespace %s against NamespaceClass %s\n", ns.Name, nc.Name)
			desired, err = c.reconcile(ctx, nc, ns)
			if err != nil {
				return err
			}
		}
	}

	checkGVKs, err := c.checkGVKs()
	if err != nil {
		return err
	}
	return c.reconciler.Prune(ctx, ns.Name, desired, checkGVKs)
}

// checkGVKs returns the union of every GVK any NamespaceClass currently in
// the cluster has ever declared, per Status.ObservedKinds.
func (c *Controller) checkGVKs() ([]schema.GroupVersionKind, error) {
	ncs, err := c.ncInformer.Lister().List(labels.Everything())
	if err != nil {
		return nil, err
	}

	seen := map[schema.GroupVersionKind]struct{}{}
	for _, nc := range ncs {
		for gvk := range observedKindSet(nc.Status.ObservedKinds) {
			seen[gvk] = struct{}{}
		}
	}

	gvks := make([]schema.GroupVersionKind, 0, len(seen))
	for gvk := range seen {
		gvks = append(gvks, gvk)
	}
	return gvks, nil
}
