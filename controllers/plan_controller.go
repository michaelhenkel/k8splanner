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
	"bytes"
	"context"
	"fmt"
	"html/template"

	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
)

// PlanReconciler reconciles a Plan object
type PlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=core.michaelhenkel,resources=plans;tasks;tasktemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.michaelhenkel,resources=plans/status;tasks/status;tasktemplates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.michaelhenkel,resources=plans/finalizers;tasks/finalizers;tasktemplates/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=pods;secrets;configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status;secrets/status;configmaps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=pods/finalizers;secrets/finalizers;configmaps/finalizers,verbs=update

//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=batch,resources=jobs/finalizers,verbs=update

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

	secret := &corev1.Secret{}
	if plan.Spec.TokenSecret != "" {
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: plan.Spec.TokenSecret}, secret); err != nil {
			if !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
	}
	if secret == nil && plan.Spec.Token != "" {
		secret.Name = req.Name
		secret.Namespace = req.Namespace
		secret.Data = map[string][]byte{"token": []byte(plan.Spec.Token)}
		if err := r.Client.Create(ctx, secret, &client.CreateOptions{}); err != nil {
			return ctrl.Result{}, err
		}
	}

	updateStatus := false

	if plan.Status.StartTime == nil {
		t := metav1.Now()
		plan.Status.StartTime = &t
		updateStatus = true
	}

	init := false
	for _, stage := range plan.Spec.Stages {
		if plan.Status.StageStatus == nil {
			var stageStatusMap = make(map[string]v1.StageStatus)
			plan.Status.StageStatus = stageStatusMap
		}
		if _, ok := plan.Status.StageStatus[stage.Name]; !ok {
			init = true
			var taskPhaseMap = make(map[string]v1.TaskPhase)
			if err := r.initializeTaskStatus(ctx, plan.Name, plan.Namespace, stage.Name, taskPhaseMap, stage.TaskTemplateReferences); err != nil {
				return ctrl.Result{}, err
			}

			plan.Status.StageStatus[stage.Name] = v1.StageStatus{
				Phase:     v1.INITIALIZED,
				TaskPhase: taskPhaseMap,
			}
			if err := r.createTasks(ctx, stage.TaskTemplateReferences, plan, req, stage.Name, secret); err != nil {
				return ctrl.Result{}, err
			}
			updateStatus = true
		}
	}

	taskTotalCounter := 0
	for _, stageStatus := range plan.Status.StageStatus {
		taskTotalCounter = taskTotalCounter + len(stageStatus.TaskPhase)

	}

	doneStagesCounter := 0
	taskDoneCounter := 0
	taskActiveCounter := 0
	if !init {
		for _, stage := range plan.Spec.Stages {
			if stageStatus, ok := plan.Status.StageStatus[stage.Name]; ok {
				switch stageStatus.Phase {
				case v1.INITIALIZED:
					currentStageStatus := plan.Status.StageStatus[stage.Name]
					currentStageStatus.Phase = v1.ACTIVE
					plan.Status.StageStatus[stage.Name] = currentStageStatus
					plan.Status.CurrentStage = stage.Name
					updateStatus = true
				case v1.FINISHED:
					doneStagesCounter++
					continue
				case v1.ACTIVE:
					var err error
					updateStatus, err = r.updateTaskStatus(ctx, stage.TaskTemplateReferences, plan, req, stage.Name, secret)
					if err != nil {
						return ctrl.Result{}, err
					}
				}

				allTasksFinished := true
				for _, taskPhase := range stageStatus.TaskPhase {
					if taskPhase.Phase != v1.FINISHED {
						allTasksFinished = false
					}
					if taskPhase.Phase == v1.ACTIVE {
						taskActiveCounter++
					}
				}
				if allTasksFinished {
					currentStageStatus := plan.Status.StageStatus[stage.Name]
					currentStageStatus.Phase = v1.FINISHED
					plan.Status.StageStatus[stage.Name] = currentStageStatus
					updateStatus = true
				}
				break
			}
		}
		for _, stage := range plan.Spec.Stages {
			if stageStatus, ok := plan.Status.StageStatus[stage.Name]; ok {
				for _, taskPhase := range stageStatus.TaskPhase {
					if taskPhase.Phase == v1.FINISHED {
						taskDoneCounter++
					}
				}
			}
		}
	}

	taskDone := fmt.Sprintf("%d/%d", taskDoneCounter, taskTotalCounter)
	if plan.Status.TasksDone != taskDone {
		plan.Status.TasksDone = taskDone
		updateStatus = true
	}

	if plan.Status.TasksActive != taskActiveCounter {
		plan.Status.TasksActive = taskActiveCounter
		updateStatus = true
	}

	doneStages := fmt.Sprintf("%d/%d", doneStagesCounter, len(plan.Spec.Stages))
	if plan.Status.StagesDone != doneStages {
		plan.Status.StagesDone = doneStages
		updateStatus = true
	}

	stageStatusFinishedCounter := 0
	for _, stageStatus := range plan.Status.StageStatus {
		if stageStatus.Phase == v1.FINISHED {
			stageStatusFinishedCounter++
		}
	}
	if stageStatusFinishedCounter == len(plan.Spec.Stages) && plan.Status.CompletionTime == nil {
		completionTime := metav1.Now()
		plan.Status.CompletionTime = &completionTime
		timeNow := metav1.Now()
		duration := timeNow.Sub(plan.Status.StartTime.Time)
		plan.Status.Duration = duration.String()
		updateStatus = true
	}

	if updateStatus {
		if err := r.Client.Status().Update(ctx, plan, &client.UpdateOptions{}); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *PlanReconciler) createTasks(ctx context.Context, TaskTemplateReferences []v1.TaskTemplateReference, plan *v1.Plan, req ctrl.Request, stageName string, tokenSecret *corev1.Secret) error {
	for _, taskTemplateRef := range TaskTemplateReferences {
		namespace := req.Namespace
		if taskTemplateRef.Namespace != "" {
			namespace = taskTemplateRef.Namespace
		}
		taskTemplate := &v1.TaskTemplate{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: taskTemplateRef.Name, Namespace: namespace}, taskTemplate); err != nil {
			klog.Error("task reference %s not found", taskTemplateRef.Name)
			return err
		}
		branch := "master"
		if plan.Spec.Branch != "" {
			branch = plan.Spec.Branch
		}
		var taskList []v1.Task
		taskVariableConfigMap := &corev1.ConfigMap{}
		if taskTemplateRef.TaskVariableConfigMap != nil {
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: plan.Namespace, Name: taskTemplateRef.TaskVariableConfigMap.Name}, taskVariableConfigMap); err != nil {
				return err
			}
			if taskVariableData, ok := taskVariableConfigMap.Data[taskTemplateRef.TaskVariableConfigMap.Key]; ok {
				var taskVariableDataMap []map[string]interface{}
				if err := yaml.Unmarshal([]byte(taskVariableData), &taskVariableDataMap); err != nil {
					return err
				}
				for idx, taskVariable := range taskVariableDataMap {
					taskRefName := taskTemplateRef.Name
					if taskTemplateRef.TaskName != "" {
						taskRefName = taskTemplateRef.TaskName
					}
					taskName := fmt.Sprintf("%s-%s-%s-%d", plan.Name, stageName, taskRefName, idx)
					task := r.defineTasks(taskName, plan.Namespace, plan.Name, branch, plan.Spec.TokenSecret, taskTemplate.Spec.Tasks, taskTemplate.Spec.Container, plan.Spec.Volumes, taskTemplateRef.CPURequest, taskTemplateRef.CPULimit)
					if taskTemplateRef.TaskVariablesMap != nil {
						for varKey, varValue := range taskTemplateRef.TaskVariablesMap {
							taskVariable[varKey] = varValue
						}
					}
					taskByte, err := yaml.Marshal(&task)
					if err != nil {
						return err
					}
					tmpl := template.Must(template.New(taskName).Parse(string(taskByte)))
					buf := &bytes.Buffer{}
					if err := tmpl.Execute(buf, taskVariable); err != nil {
						return err
					}
					if err := yaml.Unmarshal(buf.Bytes(), &task); err != nil {
						return err
					}
					taskList = append(taskList, task)
				}

			}
		} else {
			taskRefName := taskTemplateRef.Name
			if taskTemplateRef.TaskName != "" {
				taskRefName = taskTemplateRef.TaskName
			}
			task := r.defineTasks(fmt.Sprintf("%s-%s-%s", plan.Name, stageName, taskRefName), plan.Namespace, plan.Name, branch, plan.Spec.TokenSecret, taskTemplate.Spec.Tasks, taskTemplate.Spec.Container, plan.Spec.Volumes, taskTemplateRef.CPURequest, taskTemplateRef.CPULimit)
			if taskTemplateRef.TaskVariablesMap != nil {
				taskByte, err := yaml.Marshal(&task)
				if err != nil {
					return err
				}
				tmpl := template.Must(template.New(task.Name).Parse(string(taskByte)))
				buf := &bytes.Buffer{}
				if err := tmpl.Execute(buf, taskTemplateRef.TaskVariablesMap); err != nil {
					return err
				}
				if err := yaml.Unmarshal(buf.Bytes(), &task); err != nil {
					return err
				}
			}
			taskList = append(taskList, task)
		}

		for _, task := range taskList {
			if err := r.Client.Get(ctx, client.ObjectKey{Name: task.Name, Namespace: req.Namespace}, &task); err != nil {
				if errors.IsNotFound(err) {
					if err := controllerutil.SetOwnerReference(plan, &task, r.Scheme); err != nil {
						return err
					}
					if err := r.Client.Create(ctx, &task, &client.CreateOptions{}); err != nil {
						return err
					}
				} else {
					return err
				}
			}
		}
	}
	return nil
}

func (r *PlanReconciler) defineTasks(name, namespace, label, branch, tokenSecret string, tasks map[string]v1.GoTask, container *corev1.Container, volumes []corev1.Volume, cpuRequest, cpuLimit *int) v1.Task {
	run := false
	var tasksMap = make(map[string]v1.GoTask, len(tasks))
	for k, v := range tasks {
		tasksMap[k] = v
	}
	task := v1.Task{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"Plan": label},
		},
		Spec: v1.TaskSpec{
			Tasks:       tasksMap,
			Container:   container,
			Run:         &run,
			Branch:      branch,
			Volumes:     volumes,
			TokenSecret: tokenSecret,
			CPULimit:    cpuLimit,
			CPURequest:  cpuRequest,
		},
	}
	return task
}

func (r *PlanReconciler) initializeTaskStatus(ctx context.Context, planName, namespace, stageName string, taskPhaseMap map[string]v1.TaskPhase, taskTemplateReferences []v1.TaskTemplateReference) error {
	for _, taskRef := range taskTemplateReferences {
		if taskRef.Namespace != "" {
			namespace = taskRef.Namespace
		}
		taskTemplate := &v1.TaskTemplate{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: taskRef.Name, Namespace: namespace}, taskTemplate); err != nil {
			klog.Error("task reference %s not found", taskRef.Name)
			return err
		}
		taskRefName := taskRef.Name
		if taskRef.TaskName != "" {
			taskRefName = taskRef.TaskName
		}
		taskVariableConfigMap := &corev1.ConfigMap{}
		if taskRef.TaskVariableConfigMap != nil {
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: taskRef.TaskVariableConfigMap.Name}, taskVariableConfigMap); err != nil {
				return err
			}
			if taskVariableData, ok := taskVariableConfigMap.Data[taskRef.TaskVariableConfigMap.Key]; ok {
				var taskVariableDataMap []map[string]interface{}
				if err := yaml.Unmarshal([]byte(taskVariableData), &taskVariableDataMap); err != nil {
					return err
				}
				for idx, _ := range taskVariableDataMap {

					taskName := fmt.Sprintf("%s-%s-%s-%d", planName, stageName, taskRefName, idx)
					taskPhaseMap[taskName] = v1.TaskPhase{
						Phase: v1.INITIALIZED,
					}
				}

			}
		} else {
			taskPhaseMap[fmt.Sprintf("%s-%s-%s", planName, stageName, taskRefName)] = v1.TaskPhase{
				Phase: v1.INITIALIZED,
			}
		}

	}
	return nil
}

func (r *PlanReconciler) updateTaskStatus(ctx context.Context, TaskTemplateReferences []v1.TaskTemplateReference, plan *v1.Plan, req ctrl.Request, stageName string, tokenSecret *corev1.Secret) (bool, error) {
	updateStatus := false
	for _, taskRef := range TaskTemplateReferences {
		namespace := req.Namespace
		if taskRef.Namespace != "" {
			namespace = taskRef.Namespace
		}
		taskTemplate := &v1.TaskTemplate{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: taskRef.Name, Namespace: namespace}, taskTemplate); err != nil {
			klog.Error("task reference %s not found", taskRef.Name)
			return false, err
		}
		taskRefName := taskRef.Name
		if taskRef.TaskName != "" {
			taskRefName = taskRef.TaskName
		}
		var taskList []v1.Task
		taskVariableConfigMap := &corev1.ConfigMap{}
		if taskRef.TaskVariableConfigMap != nil {
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: plan.Namespace, Name: taskRef.TaskVariableConfigMap.Name}, taskVariableConfigMap); err != nil {
				return false, err
			}
			if taskVariableData, ok := taskVariableConfigMap.Data[taskRef.TaskVariableConfigMap.Key]; ok {
				var taskVariableDataMap []map[string]interface{}
				if err := yaml.Unmarshal([]byte(taskVariableData), &taskVariableDataMap); err != nil {
					return false, err
				}
				for idx, _ := range taskVariableDataMap {
					task := &v1.Task{}
					taskName := fmt.Sprintf("%s-%s-%s-%d", plan.Name, stageName, taskRefName, idx)
					if err := r.Client.Get(ctx, client.ObjectKey{Name: taskName, Namespace: req.Namespace}, task); err != nil {
						return false, err
					}
					taskList = append(taskList, *task)
				}

			}
		} else {
			task := &v1.Task{}
			if err := r.Client.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-%s-%s", plan.Name, stageName, taskRefName), Namespace: req.Namespace}, task); err != nil {
				return false, err
			}
			taskList = append(taskList, *task)
		}

		for _, task := range taskList {
			if stageStatus, ok := plan.Status.StageStatus[stageName]; ok {
				if taskPhase, ok := stageStatus.TaskPhase[task.Name]; ok {
					if taskPhase.Phase != v1.FINISHED {
						switch task.Status.State {
						case v1.WAITING, "":
							run := true
							task.Spec.Run = &run
							if err := r.Client.Update(ctx, &task, &client.UpdateOptions{}); err != nil {
								return false, err
							}
							taskPhase.Phase = v1.ACTIVE
							stageStatus.TaskPhase[task.Name] = taskPhase
							plan.Status.StageStatus[stageName] = stageStatus
							updateStatus = true
						case v1.SUCCEEDED:
							taskPhase.Phase = v1.FINISHED
							stageStatus.TaskPhase[task.Name] = taskPhase
							plan.Status.StageStatus[stageName] = stageStatus
							updateStatus = true
						}
					}
				}
			}
		}
	}
	return updateStatus, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Plan{}).
		Watches(
			&source.Kind{Type: &v1.Task{}},
			&handler.EnqueueRequestForOwner{
				OwnerType: &v1.Plan{},
				//IsController: true,
			},
			builder.WithPredicates(predicates.TaskStatusChangePredicate{
				Scheme: r.Scheme,
			}),
		).
		Complete(r)
}
