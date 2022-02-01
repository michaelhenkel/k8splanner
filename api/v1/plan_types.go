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
type Phase string
type StageState string

const (
	GIT         RepositoryType = "git"
	TGZ         RepositoryType = "tgz"
	INITIALIZED Phase          = "initialized"
	STARTED     Phase          = "started"
	ACTIVE      Phase          = "active"
	FINISHED    Phase          = "finished"
	SUCCESS     StageState     = "success"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PlanSpec defines the desired state of Plan
type PlanSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Stages      []Stage        `json:"stages,omitempty"`
	Volume      *corev1.Volume `json:"volume,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	Token       string         `json:"token,omitempty"`
	TokenSecret string         `json:"tokenSecret,omitempty"`
}

type Stage struct {
	Name                   string                  `json:"name,omitempty"`
	TaskTemplateReferences []TaskTemplateReference `json:"taskTemplateReferences,omitempty"`
	//TaskTemplates          []TaskTemplate                     `json:"taskTemplates,omitempty"`
}

type TaskTemplateReference struct {
	corev1.TypedLocalObjectReference `json:",inline"`
	TaskVariables                    *TaskVariable `json:"taskVariables,omitempty"`
}

type TaskVariable struct {
	corev1.TypedLocalObjectReference `json:",inline"`
	Key                              string `json:"key,omitempty"`
}

//`json:",inline" protobuf:"bytes,1,opt,name=objectReference"`
// PlanStatus defines the observed state of Plan
type PlanStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	StageStatus    map[string]StageStatus `json:"stageStatus,omitempty"`
	CurrentStage   string                 `json:"currentStage,omitempty"`
	StagesDone     string                 `json:"stagesDone,omitempty"`
	TasksDone      string                 `json:"tasksDone,omitempty"`
	TasksActive    int                    `json:"tasksActive,omitempty"`
	StartTime      *metav1.Time           `json:"startTime,omitempty" protobuf:"bytes,2,opt,name=startTime"`
	CompletionTime *metav1.Time           `json:"completionTime,omitempty" protobuf:"bytes,3,opt,name=completionTime"`
}

type StageStatus struct {
	Phase      Phase                `json:"phase,omitempty"`
	StageState StageState           `json:"stageState,omitempty"`
	TaskPhase  map[string]TaskPhase `json:"taskPhase,omitempty"`
}

type TaskPhase struct {
	Phase Phase `json:"phase,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Current Stage",type=string,JSONPath=`.status.currentStage`
//+kubebuilder:printcolumn:name="Start Time",type=string,JSONPath=`.status.startTime`
//+kubebuilder:printcolumn:name="Completion Time",type=string,JSONPath=`.status.completionTime`
//+kubebuilder:printcolumn:name="Tasks Active",type=integer,JSONPath=`.status.tasksActive`
//+kubebuilder:printcolumn:name="Tasks Done",type=string,JSONPath=`.status.tasksDone`
//+kubebuilder:printcolumn:name="Stages Done",type=string,JSONPath=`.status.stagesDone`
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
