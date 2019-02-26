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
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	kubeinformers "k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	exampleoperator "github.com/flugel-it/exampleoperator/pkg/apis/exampleoperator/v1alpha1"
	"github.com/flugel-it/exampleoperator/pkg/generated/clientset/versioned/fake"
	informers "github.com/flugel-it/exampleoperator/pkg/generated/informers/externalversions"
)

var (
	alwaysReady        = func() bool { return true }
	noResyncPeriodFunc = func() time.Duration { return 0 }
)

type fixture struct {
	t *testing.T

	client     *fake.Clientset
	kubeclient *k8sfake.Clientset
	// Objects to put in the store.
	immortalcontainerLister []*exampleoperator.ImmortalContainer
	podLister               []*corev1.Pod
	// Actions expected to happen on the client.
	kubeactions []core.Action
	actions     []core.Action
	// Objects from here preloaded into NewSimpleFake.
	kubeobjects []runtime.Object
	objects     []runtime.Object
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{}
	f.t = t
	f.objects = []runtime.Object{}
	f.kubeobjects = []runtime.Object{}
	return f
}

func newImmortalContainer(name string, image string) *exampleoperator.ImmortalContainer {
	return &exampleoperator.ImmortalContainer{
		TypeMeta: metav1.TypeMeta{APIVersion: exampleoperator.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: exampleoperator.ImmortalContainerSpec{
			Image: image,
		},
	}
}

func (f *fixture) newController() (*Controller, informers.SharedInformerFactory, kubeinformers.SharedInformerFactory) {
	f.client = fake.NewSimpleClientset(f.objects...)
	f.kubeclient = k8sfake.NewSimpleClientset(f.kubeobjects...)

	// Fake pod name generation
	f.kubeclient.PrependReactor("create", "pods", func(action core.Action) (bool, runtime.Object, error) {
		createAction := action.(core.CreateAction)
		pod := createAction.GetObject().(*corev1.Pod)
		podCopy := pod.DeepCopy()
		if podCopy.ObjectMeta.GenerateName != "" && podCopy.ObjectMeta.Name == "" {
			podCopy.Name = pod.ObjectMeta.GenerateName + "XXX"
		}
		return true, podCopy, nil
	})

	i := informers.NewSharedInformerFactory(f.client, noResyncPeriodFunc())
	k8sI := kubeinformers.NewSharedInformerFactory(f.kubeclient, noResyncPeriodFunc())

	c := NewController(f.kubeclient, f.client,
		k8sI.Core().V1().Pods(), i.Exampleoperator().V1alpha1().ImmortalContainers())

	c.immortalContainersSynced = alwaysReady
	c.podsSynced = alwaysReady
	c.recorder = &record.FakeRecorder{}

	for _, f := range f.immortalcontainerLister {
		i.Exampleoperator().V1alpha1().ImmortalContainers().Informer().GetIndexer().Add(f)
	}

	for _, d := range f.podLister {
		k8sI.Core().V1().Pods().Informer().GetIndexer().Add(d)
	}

	return c, i, k8sI
}

func (f *fixture) run(immortalcontainerName string) {
	f.runController(immortalcontainerName, true, false)
}

func (f *fixture) runExpectError(immortalcontainerName string) {
	f.runController(immortalcontainerName, true, true)
}

func (f *fixture) runController(immortalcontainerName string, startInformers bool, expectError bool) {
	c, i, k8sI := f.newController()
	if startInformers {
		stopCh := make(chan struct{})
		defer close(stopCh)
		i.Start(stopCh)
		k8sI.Start(stopCh)
	}

	err := c.syncHandler(immortalcontainerName)
	if !expectError && err != nil {
		f.t.Errorf("error syncing immortalcontainer: %v", err)
	} else if expectError && err == nil {
		f.t.Error("expected error syncing immortalcontainer, got nil")
	}

	actions := filterInformerActions(f.client.Actions())
	for i, action := range actions {
		if len(f.actions) < i+1 {
			f.t.Errorf("%d unexpected actions: %+v", len(actions)-len(f.actions), actions[i:])
			break
		}

		expectedAction := f.actions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.actions) > len(actions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.actions)-len(actions), f.actions[len(actions):])
	}

	k8sActions := filterInformerActions(f.kubeclient.Actions())
	for i, action := range k8sActions {
		if len(f.kubeactions) < i+1 {
			f.t.Errorf("%d unexpected actions: %+v", len(k8sActions)-len(f.kubeactions), k8sActions[i:])
			break
		}

		expectedAction := f.kubeactions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.kubeactions) > len(k8sActions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.kubeactions)-len(k8sActions), f.kubeactions[len(k8sActions):])
	}
}

// checkAction verifies that expected and actual actions are equal and both have
// same attached resources
func checkAction(expected, actual core.Action, t *testing.T) {
	if !(expected.Matches(actual.GetVerb(), actual.GetResource().Resource) && actual.GetSubresource() == expected.GetSubresource()) {
		t.Errorf("Expected\n\t%#v\ngot\n\t%#v", expected, actual)
		return
	}

	if reflect.TypeOf(actual) != reflect.TypeOf(expected) {
		t.Errorf("Action has wrong type. Expected: %t. Got: %t", expected, actual)
		return
	}

	switch a := actual.(type) {
	case core.CreateAction:
		e, _ := expected.(core.CreateAction)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expObject, object))
		}
	case core.UpdateAction:
		e, _ := expected.(core.UpdateAction)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expObject, object))
		}
	case core.PatchAction:
		e, _ := expected.(core.PatchAction)
		expPatch := e.GetPatch()
		patch := a.GetPatch()

		if !reflect.DeepEqual(expPatch, patch) {
			t.Errorf("Action %s %s has wrong patch\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expPatch, patch))
		}
	}
}

// filterInformerActions filters list and watch actions for testing resources.
// Since list and watch don't change resource state we can filter it to lower
// nose level in our tests.
func filterInformerActions(actions []core.Action) []core.Action {
	ret := []core.Action{}
	for _, action := range actions {
		if len(action.GetNamespace()) == 0 &&
			(action.Matches("list", "immortalcontainers") ||
				action.Matches("watch", "immortalcontainers") ||
				action.Matches("list", "pods") ||
				action.Matches("watch", "pods")) {
			continue
		}
		ret = append(ret, action)
	}

	return ret
}

func (f *fixture) expectCreatePodAction(d *corev1.Pod) {
	f.kubeactions = append(f.kubeactions, core.NewCreateAction(schema.GroupVersionResource{Resource: "pods"}, d.Namespace, d))
}

func (f *fixture) expectUpdatePodAction(d *corev1.Pod) {
	f.kubeactions = append(f.kubeactions, core.NewUpdateAction(schema.GroupVersionResource{Resource: "pods"}, d.Namespace, d))
}

func (f *fixture) expectUpdateImmortalContainerStatusAction(immortalcontainer *exampleoperator.ImmortalContainer) {
	action := core.NewUpdateAction(schema.GroupVersionResource{Resource: "immortalcontainers"}, immortalcontainer.Namespace, immortalcontainer)
	// TODO: Until #38113 is merged, we can't use Subresource
	//action.Subresource = "status"
	f.actions = append(f.actions, action)
}

func getKey(immortalcontainer *exampleoperator.ImmortalContainer, t *testing.T) string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(immortalcontainer)
	if err != nil {
		t.Errorf("Unexpected error getting key for immortalcontainer %v: %v", immortalcontainer.Name, err)
		return ""
	}
	return key
}

func TestCreatesPod(t *testing.T) {
	f := newFixture(t)
	immortalcontainer := newImmortalContainer("test", "nginx:latest")

	f.immortalcontainerLister = append(f.immortalcontainerLister, immortalcontainer)
	f.objects = append(f.objects, immortalcontainer)

	expPod := newPod(immortalcontainer)
	f.expectCreatePodAction(expPod)

	expectedImmortalcontainer := immortalcontainer.DeepCopy()
	expectedImmortalcontainer.Status.CurrentPod = expPod.Name

	f.expectUpdateImmortalContainerStatusAction(expectedImmortalcontainer)
	f.run(getKey(immortalcontainer, t))
}

func TestDoNothing(t *testing.T) {
	f := newFixture(t)
	immortalcontainer := newImmortalContainer("test", "nginx:latest")
	pod := newPod(immortalcontainer)
	immortalcontainer.Status.CurrentPod = pod.Name

	f.immortalcontainerLister = append(f.immortalcontainerLister, immortalcontainer)
	f.objects = append(f.objects, immortalcontainer)
	f.podLister = append(f.podLister, pod)
	f.kubeobjects = append(f.kubeobjects, pod)

	f.run(getKey(immortalcontainer, t))
}

func TestRecreatePodIfNotFound(t *testing.T) {
	f := newFixture(t)
	immortalcontainer := newImmortalContainer("test", "nginx:latest")
	immortalcontainer.Status.CurrentPod = "immortalcontainer-pod"
	f.immortalcontainerLister = append(f.immortalcontainerLister, immortalcontainer)
	f.objects = append(f.objects, immortalcontainer)

	expPod := newPod(immortalcontainer)
	f.expectCreatePodAction(expPod)

	expectedImmortalcontainer := immortalcontainer.DeepCopy()
	expectedImmortalcontainer.Status.CurrentPod = expPod.Name

	f.expectUpdateImmortalContainerStatusAction(expectedImmortalcontainer)
	f.run(getKey(immortalcontainer, t))
}

func TestNotControlledByUs(t *testing.T) {
	f := newFixture(t)
	immortalcontainer := newImmortalContainer("test", "nginx-latest")
	pod := newPod(immortalcontainer)
	immortalcontainer.Status.CurrentPod = pod.Name

	pod.ObjectMeta.OwnerReferences = []metav1.OwnerReference{}

	f.immortalcontainerLister = append(f.immortalcontainerLister, immortalcontainer)
	f.objects = append(f.objects, immortalcontainer)
	f.podLister = append(f.podLister, pod)
	f.kubeobjects = append(f.kubeobjects, pod)

	f.runExpectError(getKey(immortalcontainer, t))
}
