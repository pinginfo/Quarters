// Package features contains the feature modules of the Tenant reconciler.
//
// Adding a feature means writing one Feature and adding it to All().
package features

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

// Input is what a module reconciles: the Tenant and its resolved config.
type Input struct {
	Tenant *quartersv1alpha1.Tenant
	Config quartersv1alpha1.TenantConfig
}

// Namespace is the namespace managed for the Tenant.
func (in Input) Namespace() string { return in.Tenant.Name }

// Feature is one self-contained piece of namespace configuration.
type Feature interface {
	// Name is used for the condition type, the field manager and the module label.
	Name() string
	// Critical modules stop the run when they fail (the others depend on them).
	Critical() bool
	// Kinds lists the kinds this module creates. They are pruned when no longer
	// desired and orphaned when a Tenant with deletionPolicy Retain is deleted.
	Kinds() []schema.GroupVersionKind
	// Reconcile applies the desired objects with the Applier. Objects the module
	// stops asking for are deleted by the Applier.
	Reconcile(ctx context.Context, a *Applier, in Input) error
}

// All returns the modules in execution order.
func All() []Feature {
	return []Feature{
		Namespace{},
		Quota{},
		LimitRange{},
		Network{},
		Scheduling{},
	}
}
