package v1alpha1

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
	"github.com/pinginfo/quarters/internal/validation"
)

// SetupTenantWebhookWithManager registers the webhook for Tenant in the manager.
func SetupTenantWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &quartersv1alpha1.Tenant{}).
		WithValidator(&TenantValidator{Rules: validation.Validator{Mapper: mgr.GetRESTMapper()}}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-quarters-pinginfo-io-v1alpha1-tenant,mutating=false,failurePolicy=fail,sideEffects=None,groups=quarters.pinginfo.io,resources=tenants,verbs=create;update,versions=v1alpha1,name=vtenant-v1alpha1.kb.io,admissionReviewVersions=v1

// TenantValidator rejects invalid Tenants early: reserved names, bad quota
// keys and quantities, invalid CIDRs, incomplete scheduling rules.
//
// +kubebuilder:object:generate=false
type TenantValidator struct {
	Rules validation.Validator
}

// ValidateCreate implements admission.Validator.
func (v *TenantValidator) ValidateCreate(_ context.Context, obj *quartersv1alpha1.Tenant) (admission.Warnings, error) {
	return nil, v.invalid(obj)
}

// ValidateUpdate implements admission.Validator. Updates of a Tenant that is
// being deleted are always accepted so the finalizer can be removed.
func (v *TenantValidator) ValidateUpdate(_ context.Context, oldObj, newObj *quartersv1alpha1.Tenant) (admission.Warnings, error) {
	if !newObj.DeletionTimestamp.IsZero() {
		return nil, nil
	}
	return nil, v.invalid(newObj)
}

// ValidateDelete implements admission.Validator.
func (v *TenantValidator) ValidateDelete(_ context.Context, _ *quartersv1alpha1.Tenant) (admission.Warnings, error) {
	return nil, nil
}

func (v *TenantValidator) invalid(t *quartersv1alpha1.Tenant) error {
	if errs := v.Rules.Tenant(t); len(errs) > 0 {
		return apierrors.NewInvalid(quartersv1alpha1.GroupVersion.WithKind("Tenant").GroupKind(), t.Name, errs)
	}
	return nil
}
