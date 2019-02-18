/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	examplev1alpha1 "github.com/flugel-it/exampleoperator/pkg/apis/exampleoperator/v1alpha1"
	clientset "github.com/flugel-it/exampleoperator/pkg/generated/clientset/versioned"
	samplescheme "github.com/flugel-it/exampleoperator/pkg/generated/clientset/versioned/scheme"
	informers "github.com/flugel-it/exampleoperator/pkg/generated/informers/externalversions/exampleoperator/v1alpha1"
	listers "github.com/flugel-it/exampleoperator/pkg/generated/listers/exampleoperator/v1alpha1"
)

const controllerAgentName = "example-operator"

const (
	// SuccessSynced is used as part of the Event 'reason' when a ImmortalContainer is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a ImmortalContainer fails
	// to sync due to a Pod of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Pod already existing
	MessageResourceExists = "Resource %q already exists and is not managed by ImmortalContainer"
	// MessageResourceSynced is the message used for an Event fired when a ImmortalContainer
	// is synced successfully
	MessageResourceSynced = "ImmortalContainer synced successfully"
)

// Controller is the controller implementation for ImmortalContainer resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// sampleclientset is a clientset for our own API group
	sampleclientset clientset.Interface

	podLister                corelisters.PodLister
	podsSynced               cache.InformerSynced
	immortalContainersLister listers.ImmortalContainerLister
	immortalContainersSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new sample controller
func NewController(
	kubeclientset kubernetes.Interface,
	sampleclientset clientset.Interface,
	podInformer coreinformers.PodInformer,
	immortalContainerInformer informers.ImmortalContainerInformer) *Controller {

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(samplescheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:            kubeclientset,
		sampleclientset:          sampleclientset,
		podLister:                podInformer.Lister(),
		podsSynced:               podInformer.Informer().HasSynced,
		immortalContainersLister: immortalContainerInformer.Lister(),
		immortalContainersSynced: immortalContainerInformer.Informer().HasSynced,
		workqueue:                workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ImmortalContainers"),
		recorder:                 recorder,
	}

	klog.Info("Setting up event handlers")
	// Set up an event handler for when ImmortalContainer resources change
	immortalContainerInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueImmortalContainer,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueImmortalContainer(new)
		},
	})
	// Set up an event handler for when Pod resources change. This
	// handler will lookup the owner of the given Pod, and if it is
	// owned by a ImmortalContainer resource will enqueue that ImmortalContainer resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Pod resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {

			newDepl := new.(*corev1.Pod)
			oldDepl := old.(*corev1.Pod)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				// Periodic resync will send update events for all known Pods.
				// Two different versions of the same Pod will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting ImmortalContainer controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.podsSynced, c.immortalContainersSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process ImmortalContainer resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// ImmortalContainer resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the ImmortalContainer resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the ImmortalContainer resource with this namespace/name
	immortalContainer, err := c.immortalContainersLister.ImmortalContainers(namespace).Get(name)

	if err != nil {
		// The ImmortalContainer resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("immortal container '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	var pod *corev1.Pod
	pod = nil
	if immortalContainer.Status.CurrentPod != "" {
		pod, err = c.podLister.Pods(immortalContainer.Namespace).Get(immortalContainer.Status.CurrentPod)
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	if pod == nil {
		pod, err = c.kubeclientset.CoreV1().Pods(immortalContainer.Namespace).Create(newPod(immortalContainer))
		if err != nil {
			return err
		}
	}

	// If the Pod is not controlled by this ImmortalContainer resource, we should log
	// a warning to the event recorder and ret
	if !metav1.IsControlledBy(pod, immortalContainer) {
		msg := fmt.Sprintf(MessageResourceExists, pod.Name)
		c.recorder.Event(immortalContainer, corev1.EventTypeWarning, ErrResourceExists, msg)
		return fmt.Errorf(msg)
	}

	// Finally, we update the status block of the ImmortalContainer resource to reflect the
	// current state of the world
	err = c.updateImmortalContainerStatus(immortalContainer, pod)
	if err != nil {
		return err
	}

	c.recorder.Event(immortalContainer, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

func (c *Controller) updateImmortalContainerStatus(immortalContainer *examplev1alpha1.ImmortalContainer, pod *corev1.Pod) error {
	newStatus := calculateStatus(immortalContainer, pod)
	if reflect.DeepEqual(immortalContainer.Status, newStatus) {
		return nil
	}
	immortalContainerCopy := immortalContainer.DeepCopy()
	immortalContainerCopy.Status = newStatus

	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the ImmortalContainer resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	_, err := c.sampleclientset.ExampleoperatorV1alpha1().ImmortalContainers(immortalContainer.Namespace).Update(immortalContainerCopy)
	return err
}

func calculateStatus(immortalContainer *examplev1alpha1.ImmortalContainer, pod *corev1.Pod) examplev1alpha1.ImmortalContainerStatus {
	newStatus := examplev1alpha1.ImmortalContainerStatus{
		CurrentPod: pod.Name,
	}
	return newStatus
}

// enqueueImmortalContainer takes a ImmortalContainer resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than ImmortalContainer.
func (c *Controller) enqueueImmortalContainer(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the ImmortalContainer resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that ImmortalContainer resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a ImmortalContainer, we should not do anything more
		// with it.
		if ownerRef.Kind != "ImmortalContainer" {
			return
		}

		immortalContainer, err := c.immortalContainersLister.ImmortalContainers(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			klog.V(4).Infof("ignoring orphaned object '%s' of immortalContainer '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		if immortalContainer.Status.CurrentPod != object.GetName() {
			// Only handle events if the pod is the current pod
			// Avoid duplicate creation of pods
			return
		}

		c.enqueueImmortalContainer(immortalContainer)
		return
	}
}

// newPod creates a new Pod for a ImmortalContainer resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the ImmortalContainer resource that 'owns' it.
func newPod(immortalContainer *examplev1alpha1.ImmortalContainer) *corev1.Pod {
	labels := map[string]string{
		"controller": immortalContainer.Name,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: immortalContainer.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(immortalContainer, schema.GroupVersionKind{
					Group:   examplev1alpha1.SchemeGroupVersion.Group,
					Version: examplev1alpha1.SchemeGroupVersion.Version,
					Kind:    "ImmortalContainer",
				}),
			},
			Labels:       labels,
			GenerateName: "immortalpod-",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "acontainer",
					Image: immortalContainer.Spec.Image,
				},
			},
		},
	}
}
