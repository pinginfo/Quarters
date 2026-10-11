package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeletionPolicy says what happens to the namespace when the Tenant is deleted.
// +kubebuilder:validation:Enum=Delete;Retain
type DeletionPolicy string

const (
	// DeletionDelete deletes the namespace with the Tenant.
	DeletionDelete DeletionPolicy = "Delete"
	// DeletionRetain keeps the namespace and every object created in it.
	DeletionRetain DeletionPolicy = "Retain"
)

// Annotation and label keys used by the operator.
const (
	// AdoptAnnotation lets a Tenant take over an existing, unmanaged namespace.
	AdoptAnnotation = "quarters.pinginfo.io/adopt"
	// TenantLabel marks every object created for a Tenant.
	TenantLabel = "quarters.pinginfo.io/tenant"
	// ModuleLabel marks which feature module created an object.
	ModuleLabel = "quarters.pinginfo.io/module"
	// TeamLabel carries the team name on the namespace.
	TeamLabel = "quarters.pinginfo.io/team"
	// Finalizer protects the Tenant until its deletion policy has been applied.
	Finalizer = "quarters.pinginfo.io/finalizer"
)

// TenantSpec defines the desired state of Tenant. The namespace has the same
// name as the Tenant.
type TenantSpec struct {
	// profileRef is the name of the TenantProfile providing the defaults.
	// +kubebuilder:default=default
	// +optional
	ProfileRef string `json:"profileRef,omitempty"`

	// team owns the namespace. It becomes the quarters.pinginfo.io/team and team labels.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +required
	Team string `json:"team"`

	// labels are added to the namespace.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// annotations are added to the namespace.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// deletionPolicy is Delete or Retain.
	// +kubebuilder:default=Retain
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// TenantConfig overrides the profile.
	TenantConfig `json:",inline"`
}

// TenantStatus defines the observed state of Tenant.
type TenantStatus struct {
	// observedGeneration is the generation the conditions refer to.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// phase is Ready, Degraded or Terminating.
	// +optional
	Phase string `json:"phase,omitempty"`

	// resolved is the profile merged with the Tenant overrides, as applied.
	// +optional
	Resolved *TenantConfig `json:"resolved,omitempty"`

	// conditions has Ready plus one condition per feature module.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Team",type=string,JSONPath=".spec.team"
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=".spec.profileRef"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Tenant is a namespace and everything the platform configures inside it.
type Tenant struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TenantSpec `json:"spec"`

	// +optional
	Status TenantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantList contains a list of Tenant
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Tenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Tenant{}, &TenantList{})
		return nil
	})
}
