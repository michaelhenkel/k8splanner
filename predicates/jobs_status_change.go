package predicates

import (
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PodIPChangedPredicate implements a default update predicate function on
// PodIP status state change.
type JobStatusChangePredicate struct {
	predicate.Funcs
	Scheme *runtime.Scheme
}

// Update implements default UpdateEvent filter for validating state change
func (p JobStatusChangePredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		return false
	}
	oldJob, ok := e.ObjectOld.(*batchv1.Job)
	if !ok {
		return false
	}
	if e.ObjectNew == nil {
		return false
	}
	newJob, ok := e.ObjectNew.(*batchv1.Job)
	if !ok {
		return false
	}
	if _, ok := newJob.Labels["k8splanner"]; ok {
		if len(newJob.Status.Conditions) == 0 {
			return false
		}
		oldConditionComplete := false
		for _, oldCondition := range oldJob.Status.Conditions {
			if oldCondition.Type == batchv1.JobComplete && oldCondition.Status == "True" {
				oldConditionComplete = true
			}
		}
		newConditionComplete := false
		for _, newCondition := range newJob.Status.Conditions {
			if newCondition.Type == batchv1.JobComplete && newCondition.Status == "True" {
				newConditionComplete = true
			}
		}
		return !oldConditionComplete == newConditionComplete
	}
	return false
}
