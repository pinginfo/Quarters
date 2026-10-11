package features

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	quotaGVK      = corev1.SchemeGroupVersion.WithKind("ResourceQuota")
	limitRangeGVK = corev1.SchemeGroupVersion.WithKind("LimitRange")
)

// Quota manages the ResourceQuota <namespace>-quota.
type Quota struct{}

func (Quota) Name() string                     { return "Quota" }
func (Quota) Critical() bool                   { return false }
func (Quota) Kinds() []schema.GroupVersionKind { return []schema.GroupVersionKind{quotaGVK} }

func (q Quota) Reconcile(ctx context.Context, a *Applier, in Input) error {
	var desired []*unstructured.Unstructured
	if len(in.Config.Quota) > 0 {
		hard := corev1.ResourceList{}
		for k, v := range in.Config.Quota {
			hard[corev1.ResourceName(k)] = v
		}
		u, err := toUnstructured(&corev1.ResourceQuota{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ResourceQuota"},
			ObjectMeta: metav1.ObjectMeta{Name: in.Namespace() + "-quota", Namespace: in.Namespace()},
			Spec:       corev1.ResourceQuotaSpec{Hard: hard},
		})
		if err != nil {
			return err
		}
		desired = append(desired, u)
	}
	return a.Apply(ctx, in.Tenant, "quota", q.Kinds(), desired)
}

// LimitRange manages the container defaults in <namespace>-quota-limit-range.
type LimitRange struct{}

func (LimitRange) Name() string                     { return "LimitRange" }
func (LimitRange) Critical() bool                   { return false }
func (LimitRange) Kinds() []schema.GroupVersionKind { return []schema.GroupVersionKind{limitRangeGVK} }

func (l LimitRange) Reconcile(ctx context.Context, a *Applier, in Input) error {
	var desired []*unstructured.Unstructured
	if lr := in.Config.LimitRange; lr != nil {
		u, err := toUnstructured(&corev1.LimitRange{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "LimitRange"},
			ObjectMeta: metav1.ObjectMeta{Name: in.Namespace() + "-quota-limit-range", Namespace: in.Namespace()},
			Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
				Type:           corev1.LimitTypeContainer,
				Default:        lr.Default,
				DefaultRequest: lr.DefaultRequest,
				Max:            lr.Max,
				Min:            lr.Min,
			}}},
		})
		if err != nil {
			return err
		}
		desired = append(desired, u)
	}
	return a.Apply(ctx, in.Tenant, "limitrange", l.Kinds(), desired)
}
