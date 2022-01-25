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
	"reflect"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "michaelhenkel/k8splanner/api/v1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
)

var (
	taskCommandList = []string{
		"sh",
		"-c",
		"while true; do sleep 10;done",
	}
)

const (
	codePullImage = "busybox:latest"
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

	for _, stage := range plan.Spec.Stages {
		for _, taskRef := range stage.TaskReferences {
			task := &v1.Task{}
			if err := r.Client.Get(ctx, client.ObjectKey{Name: taskRef.Name, Namespace: req.Namespace}, task); err != nil {
				klog.Error("task reference %s not found", taskRef.Name)
				return ctrl.Result{}, err
			}
			/*
				tspec := v1.TaskSpec{

					Tasks: map[string]*taskfile.Task{"bla": &taskfile.Task{
						Cmds: []*taskfile.Cmd{{
							Cmd: "bla",
						}},
					}},
				}
			*/
			taskByte, err := yaml.Marshal(task)
			if err != nil {
				return ctrl.Result{}, err
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
						return ctrl.Result{}, err
					}
					if err := r.Client.Create(ctx, taskConfigMap, &client.CreateOptions{}); err != nil {
						return ctrl.Result{}, err
					}
				}
			} else {
				if !reflect.DeepEqual(taskConfigMap.Data, foundConfigMap.Data) {
					if err := r.Client.Update(ctx, taskConfigMap, &client.UpdateOptions{}); err != nil {
						return ctrl.Result{}, err
					}
				}
			}
			job := r.defineJob(task, plan.Spec.Volume, taskConfigMap)
			foundJob := &batchv1.Job{}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(job), foundJob); err != nil {
				if errors.IsNotFound(err) {
					if err := controllerutil.SetControllerReference(plan, job, r.Scheme); err != nil {
						return reconcile.Result{}, err
					}
					if err := r.Create(ctx, job, &client.CreateOptions{}); err != nil {
						klog.Errorf("failed to create code pull job %s with error %s", job.Name, err)
						return ctrl.Result{}, err
					}
				} else {
					return ctrl.Result{}, err
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *PlanReconciler) defineJob(task *v1.Task, volume *corev1.Volume, configMap *corev1.ConfigMap) *batchv1.Job {
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
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      task.Name,
			Namespace: task.Namespace,
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
		Complete(r)
}
