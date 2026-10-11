package v1alpha1

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
	"github.com/pinginfo/quarters/internal/validation"
)

// SetupTenantProfileWebhookWithManager registers the webhook for TenantProfile in the manager.
func SetupTenantProfileWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &quartersv1alpha1.TenantProfile{}).
		WithValidator(&TenantProfileValidator{Rules: validation.Validator{Mapper: mgr.GetRESTMapper()}}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-quarters-pinginfo-io-v1alpha1-tenantprofile,mutating=false,failurePolicy=fail,sideEffects=None,groups=quarters.pinginfo.io,resources=tenantprofiles,verbs=create;update,versions=v1alpha1,name=vtenantprofile-v1alpha1.kb.io,admissionReviewVersions=v1

// TenantProfileValidator applies the same rules as Tenants to profile defaults.
//
// +kubebuilder:object:generate=false
type TenantProfileValidator struct {
	Rules validation.Validator
}

// ValidateCreate implements admission.Validator.
func (v *TenantProfileValidator) ValidateCreate(_ context.Context, obj *quartersv1alpha1.TenantProfile) (admission.Warnings, error) {
	return nil, v.invalid(obj)
}

// ValidateUpdate implements admission.Validator.
func (v *TenantProfileValidator) ValidateUpdate(_ context.Context, _, newObj *quartersv1alpha1.TenantProfile) (admission.Warnings, error) {
	return nil, v.invalid(newObj)
}

// ValidateDelete implements admission.Validator.
func (v *TenantProfileValidator) ValidateDelete(_ context.Context, _ *quartersv1alpha1.TenantProfile) (admission.Warnings, error) {
	return nil, nil
}

func (v *TenantProfileValidator) invalid(p *quartersv1alpha1.TenantProfile) error {
	if errs := v.Rules.Profile(p); len(errs) > 0 {
		return apierrors.NewInvalid(quartersv1alpha1.GroupVersion.WithKind("TenantProfile").GroupKind(), p.Name, errs)
	}
	return nil
}
