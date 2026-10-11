package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TenantProfileSpec holds the defaults a Tenant inherits (the old values.yaml
// "defaults" block). A Tenant only states what differs.
type TenantProfileSpec struct {
	TenantConfig `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

// TenantProfile is a named set of defaults referenced by Tenants.
type TenantProfile struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TenantProfileSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// TenantProfileList contains a list of TenantProfile
type TenantProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TenantProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TenantProfile{}, &TenantProfileList{})
		return nil
	})
}
