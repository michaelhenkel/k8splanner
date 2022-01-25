/*
Copyright 2022.

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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RepositoryType string

const (
	GIT RepositoryType = "git"
	TGZ RepositoryType = "tgz"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PlanSpec defines the desired state of Plan
type PlanSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Reositories is a list of code repositories used in the plan
	Repositories          []Repository                      `json:"repositories,omitempty"`
	Stages                map[string]Stage                  `json:"stages,omitempty"`
	Volume                *corev1.Volume                    `json:"volume,omitempty"`
	CodePullTaskReference *corev1.TypedLocalObjectReference `json:"codePullTaskReference,omitempty"`
}

type Stage struct {
	TaskReferences []corev1.TypedLocalObjectReference `json:"taskReferences,omitempty"`
}

// Repository defines a location from which the source code is downloaded
type Repository struct {
	Name               string         `json:"name,omitempty"`
	Type               RepositoryType `json:"type,omitempty"`
	Location           string         `json:"location,omitempty"`
	TokenSecret        string         `json:"tokenSecret,omitempty"`
	UserPasswordSecret string         `json:"userPasswordSecret,omitempty"`
}

// PlanStatus defines the observed state of Plan
type PlanStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Condition string `json:"condition,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Plan is the Schema for the plans API
type Plan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlanSpec   `json:"spec,omitempty"`
	Status PlanStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PlanList contains a list of Plan
type PlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Plan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Plan{}, &PlanList{})
}
