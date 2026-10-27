package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TenantConfig holds everything that can be defaulted by a TenantProfile and
// overridden by a Tenant. Maps are merged key by key, lists and scalars are
// replaced. Every field is optional so that a Tenant only states what differs
// from its profile.
type TenantConfig struct {
	// quota is the ResourceQuota content, keyed by resource name, for example
	// "requests.cpu", "pods" or "count/statefulsets.apps".
	// +optional
	Quota map[string]resource.Quantity `json:"quota,omitempty"`

	// limitRange configures the per-container LimitRange.
	// +optional
	LimitRange *LimitRangeSpec `json:"limitRange,omitempty"`

	// podSecurity sets the Pod Security Admission labels on the namespace.
	// +optional
	PodSecurity *PodSecuritySpec `json:"podSecurity,omitempty"`

	// network configures the Cilium network policies of the namespace.
	// +optional
	Network *NetworkSpec `json:"network,omitempty"`

	// scheduling forces or restricts node placement of the pods of the namespace.
	// +optional
	Scheduling *SchedulingSpec `json:"scheduling,omitempty"`
}

// LimitRangeSpec describes the container defaults and bounds.
type LimitRangeSpec struct {
	// default is the limit applied to containers that do not set one.
	// +optional
	Default corev1.ResourceList `json:"default,omitempty"`
	// defaultRequest is the request applied to containers that do not set one.
	// +optional
	DefaultRequest corev1.ResourceList `json:"defaultRequest,omitempty"`
	// max is the maximum a container may set.
	// +optional
	Max corev1.ResourceList `json:"max,omitempty"`
	// min is the minimum a container must set.
	// +optional
	Min corev1.ResourceList `json:"min,omitempty"`
}

// PodSecurityLevel is a Pod Security Standard level.
// +kubebuilder:validation:Enum=privileged;baseline;restricted
type PodSecurityLevel string

// PodSecuritySpec maps to the pod-security.kubernetes.io/* namespace labels.
type PodSecuritySpec struct {
	// +optional
	Enforce PodSecurityLevel `json:"enforce,omitempty"`
	// +optional
	Audit PodSecurityLevel `json:"audit,omitempty"`
	// +optional
	Warn PodSecurityLevel `json:"warn,omitempty"`
}

// NetworkSpec groups the ingress and egress rules.
type NetworkSpec struct {
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`
	// +optional
	Egress *EgressSpec `json:"egress,omitempty"`
}

// IngressSpec lists what may reach the pods. When nothing is allowed, all
// ingress is denied.
type IngressSpec struct {
	// allowSameNamespace allows traffic between pods of the namespace.
	// +optional
	AllowSameNamespace *bool `json:"allowSameNamespace,omitempty"`
	// allowCiliumIngress allows the Cilium "ingress" entity (Cilium Ingress / Gateway).
	// +optional
	AllowCiliumIngress *bool `json:"allowCiliumIngress,omitempty"`
	// allowFromNamespaces lists namespace names that may reach the pods.
	// +optional
	AllowFromNamespaces []string `json:"allowFromNamespaces,omitempty"`
}

// EgressSpec lists where the pods may connect to. When nothing is allowed, all
// egress is denied. DNS to kube-dns is always allowed once any egress is.
type EgressSpec struct {
	// allowAll allows all egress traffic.
	// +optional
	AllowAll *bool `json:"allowAll,omitempty"`
	// toNamespaces lists namespace names the pods may reach.
	// +optional
	ToNamespaces []string `json:"toNamespaces,omitempty"`
	// toCIDRs lists CIDR ranges the pods may reach.
	// +optional
	ToCIDRs []CIDRRule `json:"toCIDRs,omitempty"`
	// toFQDNs lists DNS names the pods may reach.
	// +optional
	ToFQDNs []FQDNRule `json:"toFQDNs,omitempty"`
	// allowKubeAPIServer allows the Cilium "kube-apiserver" entity.
	// +optional
	AllowKubeAPIServer *bool `json:"allowKubeAPIServer,omitempty"`
}

// CIDRRule allows a CIDR range minus some exceptions.
type CIDRRule struct {
	// +required
	CIDR string `json:"cidr"`
	// +optional
	Except []string `json:"except,omitempty"`
}

// FQDNRule allows a DNS name (exact or pattern) on some ports.
type FQDNRule struct {
	// +optional
	MatchName string `json:"matchName,omitempty"`
	// +optional
	MatchPattern string `json:"matchPattern,omitempty"`
	// +optional
	Ports []PortRule `json:"ports,omitempty"`
}

// PortRule is a port and protocol.
type PortRule struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// +kubebuilder:validation:Enum=TCP;UDP;SCTP
	// +kubebuilder:default=TCP
	// +optional
	Protocol string `json:"protocol,omitempty"`
}

// SchedulingMode tells how a scheduling constraint is applied.
//   - Force: injected into every pod of the namespace (Gatekeeper mutation).
//   - Allow: pods may use it, anything else is rejected (Gatekeeper validation).
//   - Disabled: nothing is injected or enforced.
//
// +kubebuilder:validation:Enum=Force;Allow;Disabled
type SchedulingMode string

const (
	SchedulingForce    SchedulingMode = "Force"
	SchedulingAllow    SchedulingMode = "Allow"
	SchedulingDisabled SchedulingMode = "Disabled"
)

// SchedulingSpec configures node selectors and tolerations independently.
type SchedulingSpec struct {
	// +optional
	NodeSelector *NodeSelectorSpec `json:"nodeSelector,omitempty"`
	// +optional
	Tolerations *TolerationsSpec `json:"tolerations,omitempty"`
}

// NodeSelectorSpec is a generic label selector, for example {location: site-a}.
type NodeSelectorSpec struct {
	// +kubebuilder:default=Disabled
	// +optional
	Mode SchedulingMode `json:"mode,omitempty"`
	// labels are the node labels. Any key is accepted.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// TolerationsSpec is a generic set of tolerations.
type TolerationsSpec struct {
	// +kubebuilder:default=Disabled
	// +optional
	Mode SchedulingMode `json:"mode,omitempty"`
	// items are the tolerations. In Force mode each one needs a key.
	// +optional
	Items []corev1.Toleration `json:"items,omitempty"`
}
