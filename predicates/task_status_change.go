package predicates

import (
	v1 "michaelhenkel/k8splanner/api/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PodIPChangedPredicate implements a default update predicate function on
// PodIP status state change.
type TaskStatusChangePredicate struct {
	predicate.Funcs
	Scheme *runtime.Scheme
}

// Update implements default UpdateEvent filter for validating state change
func (p TaskStatusChangePredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		return false
	}
	oldTask, ok := e.ObjectOld.(*v1.Task)
	if !ok {
		return false
	}
	if e.ObjectNew == nil {
		return false
	}
	newTask, ok := e.ObjectNew.(*v1.Task)
	if !ok {
		return false
	}
	return oldTask.Status.State != newTask.Status.State
}
