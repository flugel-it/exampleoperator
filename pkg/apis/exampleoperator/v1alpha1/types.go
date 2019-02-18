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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ImmortalContainer is a specification for an ImmortalContainer resource
type ImmortalContainer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImmortalContainerSpec   `json:"spec"`
	Status ImmortalContainerStatus `json:"status,omitempty"`
}

// ImmortalContainerSpec is the spec for an ImmortalContainer resource
type ImmortalContainerSpec struct {
	Image string `json:"image"`
}

// ImmortalContainerStatus is the status for an ImmortalContainer resource
type ImmortalContainerStatus struct {
	CurrentPod string `json:"currentPod"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ImmortalContainerList is a list of ImmortalContainer resources
type ImmortalContainerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []ImmortalContainer `json:"items"`
}
