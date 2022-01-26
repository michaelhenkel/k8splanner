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

package controllers

import (
	"context"
	"fmt"
	"reflect"

	"github.com/ghodss/yaml"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	v1 "michaelhenkel/k8splanner/api/v1"
	"michaelhenkel/k8splanner/predicates"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
)

// PlanReconciler reconciles a Plan object
type PlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=core.michaelhenkel,resources=plans,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.michaelhenkel,resources=plans/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.michaelhenkel,resources=plans/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Plan object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *PlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	plan := &v1.Plan{}
	if err := r.Client.Get(ctx, req.NamespacedName, plan); err != nil && !errors.IsNotFound(err) {
		klog.Errorf("Plan %s exists but cannot be retrieved", req.Name)
		return ctrl.Result{}, err
	}

	updateStatus := false
	for _, stage := range plan.Spec.Stages {
		if plan.Status.StageStatus == nil {
			var stageStatusMap = make(map[string]v1.StageStatus)
			plan.Status.StageStatus = stageStatusMap
		}
		if _, ok := plan.Status.StageStatus[stage.Name]; !ok {
			var taskPhaseMap = make(map[string]v1.TaskPhase)
			for _, taskRef := range stage.TaskReferences {
				taskPhaseMap[taskRef.Name] = v1.TaskPhase{
					Phase: v1.INITIALIZED,
				}
			}
			plan.Status.StageStatus[stage.Name] = v1.StageStatus{
				Phase:     v1.INITIALIZED,
				TaskPhase: taskPhaseMap,
			}
			updateStatus = true
		}
	}
	if updateStatus {
		if err := r.Client.Status().Update(ctx, plan, &client.UpdateOptions{}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	for _, stage := range plan.Spec.Stages {
		if stageStatus, ok := plan.Status.StageStatus[stage.Name]; ok {
			manageTask := false
			switch stageStatus.Phase {
			case v1.INITIALIZED:
				currentStageStatus := plan.Status.StageStatus[stage.Name]
				currentStageStatus.Phase = v1.RUNNING
				plan.Status.StageStatus[stage.Name] = currentStageStatus
				plan.Status.CurrentStage = stage.Name
				if err := r.Client.Status().Update(ctx, plan, &client.UpdateOptions{}); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{Requeue: true}, nil
				//manageTask = true
			case v1.FINISHED:
				continue
			case v1.RUNNING:
				manageTask = true
			}
			if manageTask {
				updateStatus, err := r.manageTasks(ctx, stage.TaskReferences, plan, req, stage.Name)
				if err != nil {
					return ctrl.Result{}, err
				}
				if updateStatus {
					if err := r.Client.Status().Update(ctx, plan, &client.UpdateOptions{}); err != nil {
						return ctrl.Result{}, err
					}
					fmt.Println("updated task")
					return ctrl.Result{Requeue: true}, nil
				}
			}
			allTasksFinished := true
			for _, taskPhase := range stageStatus.TaskPhase {
				if taskPhase.Phase != v1.FINISHED {
					allTasksFinished = false
				}
			}
			if allTasksFinished {
				currentStageStatus := plan.Status.StageStatus[stage.Name]
				currentStageStatus.Phase = v1.FINISHED
				plan.Status.StageStatus[stage.Name] = currentStageStatus
				if err := r.Client.Status().Update(ctx, plan, &client.UpdateOptions{}); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{Requeue: true}, nil
			} else {
				return ctrl.Result{}, nil
			}
		}

	}
	return ctrl.Result{}, nil
}

func (r *PlanReconciler) manageTasks(ctx context.Context, TaskReferences []corev1.TypedLocalObjectReference, plan *v1.Plan, req ctrl.Request, stageName string) (bool, error) {
	updateStatus := false
	for _, taskRef := range TaskReferences {
		task := &v1.Task{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: taskRef.Name, Namespace: req.Namespace}, task); err != nil {
			klog.Error("task reference %s not found", taskRef.Name)
			return false, err
		}
		if stageStatus, ok := plan.Status.StageStatus[stageName]; ok {
			if taskPhase, ok := stageStatus.TaskPhase[task.Name]; ok {
				switch taskPhase.Phase {
				case v1.FINISHED:
					continue
				}
			}
		}
		t := struct {
			Version string               `json:"version,omitempty"`
			Tasks   map[string]v1.GoTask `json:"tasks,omitempty"`
		}{
			Version: "3",
			Tasks:   task.Spec.Tasks,
		}

		taskByte, err := yaml.Marshal(&t)
		if err != nil {
			return false, err
		}
		taskConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      taskRef.Name,
				Namespace: req.Namespace,
			},
			Data: map[string]string{"task": string(taskByte)},
		}

		foundConfigMap := &corev1.ConfigMap{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: taskRef.Name}, foundConfigMap); err != nil {
			if errors.IsNotFound(err) {
				if err := controllerutil.SetOwnerReference(plan, taskConfigMap, r.Scheme); err != nil {
					return false, err
				}
				if err := r.Client.Create(ctx, taskConfigMap, &client.CreateOptions{}); err != nil {
					return false, err
				}
			}
		} else {
			if !reflect.DeepEqual(taskConfigMap.Data, foundConfigMap.Data) {
				if err := r.Client.Update(ctx, taskConfigMap, &client.UpdateOptions{}); err != nil {
					return false, err
				}
			}
		}
		job := r.defineJob(task, plan.Spec.Volume, taskConfigMap, plan.Name)
		foundJob := &batchv1.Job{}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(job), foundJob); err != nil {
			if errors.IsNotFound(err) {
				if err := controllerutil.SetControllerReference(plan, job, r.Scheme); err != nil {
					return false, err
				}
				if err := r.Create(ctx, job, &client.CreateOptions{}); err != nil {
					klog.Errorf("failed to create code pull job %s with error %s", job.Name, err)
					return false, err
				}
				if stageStatus, ok := plan.Status.StageStatus[stageName]; ok {
					if taskPhase, ok := stageStatus.TaskPhase[task.Name]; ok {
						switch taskPhase.Phase {
						case v1.INITIALIZED:
							updateStatus = true
							taskPhase.Phase = v1.RUNNING
							stageStatus.TaskPhase[task.Name] = taskPhase
						}
					}
				}
			} else {
				return false, err
			}
		} else {
			for _, condition := range foundJob.Status.Conditions {
				if condition.Type == batchv1.JobComplete && condition.Status == "True" {
					if stageStatus, ok := plan.Status.StageStatus[stageName]; ok {
						if taskPhase, ok := stageStatus.TaskPhase[task.Name]; ok {
							updateStatus = true
							taskPhase.Phase = v1.FINISHED
							stageStatus.TaskPhase[task.Name] = taskPhase
						}
					}

				}
			}
		}
	}

	return updateStatus, nil
}

func (r *PlanReconciler) defineJob(task *v1.Task, volume *corev1.Volume, configMap *corev1.ConfigMap, planeName string) *batchv1.Job {
	taskVolume := corev1.Volume{
		Name: "task",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMap.Name,
				},
			},
		},
	}
	task.Spec.Container.VolumeMounts = append(task.Spec.Container.VolumeMounts, corev1.VolumeMount{
		Name:      "task",
		MountPath: "/var/task",
	})
	task.Spec.Container.VolumeMounts = append(task.Spec.Container.VolumeMounts, corev1.VolumeMount{
		Name:      volume.Name,
		MountPath: fmt.Sprintf("/mnt/%s", volume.Name),
	})
	task.Spec.Container.Env = []corev1.EnvVar{{
		Name: "PodName",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.name",
			},
		},
	}, {
		Name: "PodNamespace",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.namespace",
			},
		},
	}, {
		Name:  "Plan",
		Value: planeName,
	}}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      task.Name,
			Namespace: task.Namespace,
			Labels:    map[string]string{"k8splanner": task.Name},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: task.ObjectMeta,
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{*task.Spec.Container},
					Volumes:       []corev1.Volume{*volume, taskVolume},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
	return job
}

// SetupWithManager sets up the controller with the Manager.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Plan{}).
		Watches(
			&source.Kind{Type: &batchv1.Job{}},
			&handler.EnqueueRequestForOwner{
				OwnerType:    &v1.Plan{},
				IsController: true,
			},
			builder.WithPredicates(predicates.JobStatusChangePredicate{
				Scheme: r.Scheme,
			}),
		).
		Complete(r)
}
