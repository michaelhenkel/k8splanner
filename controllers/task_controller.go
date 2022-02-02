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
	"strconv"

	"github.com/ghodss/yaml"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	v1 "michaelhenkel/k8splanner/api/v1"
	"michaelhenkel/k8splanner/predicates"
)

// TaskReconciler reconciles a Task object
type TaskReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=core.michaelhenkel,resources=tasks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.michaelhenkel,resources=tasks/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.michaelhenkel,resources=tasks/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=pods;secrets;configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status;secrets/status;configmaps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=pods/finalizers;secrets/finalizers;configmaps/finalizers,verbs=update

//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=batch,resources=jobs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Task object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *TaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	task := &v1.Task{}
	if err := r.Client.Get(ctx, req.NamespacedName, task); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
	}

	run := true
	if task.Spec.Run != nil && !*task.Spec.Run {
		run = false
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
		return reconcile.Result{}, err
	}
	taskConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      task.Name,
			Namespace: req.Namespace,
		},
		Data: map[string]string{"task": string(taskByte)},
	}

	foundConfigMap := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: task.Name}, foundConfigMap); err != nil {
		if errors.IsNotFound(err) {
			if err := controllerutil.SetOwnerReference(task, taskConfigMap, r.Scheme); err != nil {
				return reconcile.Result{}, err
			}
			if err := r.Client.Create(ctx, taskConfigMap, &client.CreateOptions{}); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		if !reflect.DeepEqual(taskConfigMap.Data, foundConfigMap.Data) {
			if err := r.Client.Update(ctx, taskConfigMap, &client.UpdateOptions{}); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	secret := &corev1.Secret{}
	if task.Spec.TokenSecret != "" {
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: task.Spec.TokenSecret}, secret); err != nil {
			if !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			} else {
				secret = nil
			}
		}
	} else {
		secret = nil
	}

	if run {
		job := r.defineJob(task, taskConfigMap, secret)
		foundJob := &batchv1.Job{}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(job), foundJob); err != nil {
			if errors.IsNotFound(err) {
				if err := controllerutil.SetControllerReference(task, job, r.Scheme); err != nil {
					return reconcile.Result{}, err
				}
				if err := r.Create(ctx, job, &client.CreateOptions{}); err != nil {
					klog.Errorf("failed to create code pull job %s with error %s", job.Name, err)
					return reconcile.Result{}, err
				}
			} else {
				return reconcile.Result{}, err
			}
		} else {
			task.Status.Active = foundJob.Status.Active
			task.Status.Conditions = foundJob.Status.Conditions
			task.Status.StartTime = foundJob.Status.StartTime
			task.Status.Succeeded = foundJob.Status.Succeeded
			task.Status.Failed = foundJob.Status.Failed
			if task.Status.Active == 1 {
				task.Status.State = v1.RUNNING
			} else if task.Status.Succeeded > 0 && task.Status.CompletionTime == nil {
				task.Status.CompletionTime = foundJob.Status.CompletionTime
				task.Status.State = v1.SUCCEEDED
				timeNow := metav1.Now()
				duration := timeNow.Sub(task.Status.StartTime.Time)
				task.Status.Duration = duration.String()
			}
			if err := r.Client.Status().Update(ctx, task, &client.UpdateOptions{}); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		task.Status.State = v1.WAITING
		if err := r.Client.Status().Update(ctx, task, &client.UpdateOptions{}); err != nil {
			return reconcile.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *TaskReconciler) defineJob(task *v1.Task, configMap *corev1.ConfigMap, tokenSecret *corev1.Secret) *batchv1.Job {

	var volumeList []corev1.Volume
	if task.Spec.Volumes != nil {
		for _, volume := range task.Spec.Volumes {
			volumeList = append(volumeList, volume)
			task.Spec.Container.VolumeMounts = append(task.Spec.Container.VolumeMounts, corev1.VolumeMount{
				Name:      volume.Name,
				MountPath: fmt.Sprintf("/mnt/%s", volume.Name),
			})
		}
	}
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
	volumeList = append(volumeList, taskVolume)
	task.Spec.Container.VolumeMounts = append(task.Spec.Container.VolumeMounts, corev1.VolumeMount{
		Name:      "task",
		MountPath: "/var/task",
	})

	if tokenSecret != nil {
		task.Spec.Container.VolumeMounts = append(task.Spec.Container.VolumeMounts, corev1.VolumeMount{
			Name:      "token",
			MountPath: "/var/token",
		})
		tokenVolume := corev1.Volume{
			Name: "token",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: tokenSecret.Name,
				},
			},
		}
		volumeList = append(volumeList, tokenVolume)
	}
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
	}}
	branch := "master"
	if task.Spec.Branch != "" {
		branch = task.Spec.Branch
	}
	task.Spec.Container.Env = append(task.Spec.Container.Env, corev1.EnvVar{
		Name:  "Branch",
		Value: branch,
	})
	if planLabel, ok := task.Labels["Plan"]; ok {
		task.Spec.Container.Env = append(task.Spec.Container.Env, corev1.EnvVar{
			Name:  "Plan",
			Value: planLabel,
		})
	}
	resourceRequirements := corev1.ResourceRequirements{}
	if task.Spec.CPULimit != nil {
		resourceRequirements.Limits = corev1.ResourceList{
			"cpu": resource.MustParse(strconv.Itoa(*task.Spec.CPULimit)),
		}
		task.Spec.Container.Resources = resourceRequirements
		task.Spec.Container.Env = append(task.Spec.Container.Env, corev1.EnvVar{
			Name:  "cpurequest",
			Value: strconv.Itoa(*task.Spec.CPULimit),
		})
	}
	if task.Spec.CPURequest != nil {
		resourceRequirements.Requests = corev1.ResourceList{
			"cpu": resource.MustParse(strconv.Itoa(*task.Spec.CPURequest)),
		}
		task.Spec.Container.Resources = resourceRequirements
		task.Spec.Container.Env = append(task.Spec.Container.Env, corev1.EnvVar{
			Name:  "cpurequest",
			Value: strconv.Itoa(*task.Spec.CPURequest),
		})
	}

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
					Volumes:       volumeList,
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
	return job
}

// SetupWithManager sets up the controller with the Manager.
func (r *TaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		// For().
		For(&v1.Task{}).
		Watches(
			&source.Kind{Type: &batchv1.Job{}},
			&handler.EnqueueRequestForOwner{
				OwnerType:    &v1.Task{},
				IsController: true,
			},
			builder.WithPredicates(predicates.JobStatusChangePredicate{
				Scheme: r.Scheme,
			}),
		).
		Complete(r)
}
