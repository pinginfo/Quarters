package features

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

// ErrNamespaceUnmanaged is returned when the namespace exists but was not
// created by Quarters and the Tenant has no adopt annotation.
var ErrNamespaceUnmanaged = errors.New("namespace already exists and is not managed by quarters; annotate the Tenant with " + quartersv1alpha1.AdoptAnnotation + "=true to take it over")

// Namespace creates the namespace with its labels, annotations and the Pod
// Security Admission labels.
type Namespace struct{}

func (Namespace) Name() string                     { return "Namespace" }
func (Namespace) Critical() bool                   { return true }
func (Namespace) Kinds() []schema.GroupVersionKind { return nil }

func (Namespace) Reconcile(ctx context.Context, a *Applier, in Input) error {
	t := in.Tenant

	existing := &corev1.Namespace{}
	err := a.Client.Get(ctx, client.ObjectKey{Name: t.Name}, existing)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	default:
		if existing.DeletionTimestamp != nil {
			return fmt.Errorf("namespace %s is terminating", t.Name)
		}
		managed := existing.Labels[ManagedByLabel] == ManagedByValue && existing.Labels[quartersv1alpha1.TenantLabel] == t.Name
		if !managed && t.Annotations[quartersv1alpha1.AdoptAnnotation] != "true" {
			return ErrNamespaceUnmanaged
		}
	}

	labels := map[string]string{}
	for k, v := range t.Spec.Labels {
		labels[k] = v
	}
	labels["team"] = t.Spec.Team
	labels[quartersv1alpha1.TeamLabel] = t.Spec.Team
	if ps := in.Config.PodSecurity; ps != nil {
		setPSA(labels, "enforce", ps.Enforce)
		setPSA(labels, "audit", ps.Audit)
		setPSA(labels, "warn", ps.Warn)
	}

	ns := &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: t.Name, Labels: labels, Annotations: t.Spec.Annotations},
	}
	u, err := toUnstructured(ns)
	if err != nil {
		return err
	}
	return a.Apply(ctx, t, "namespace", nil, []*unstructured.Unstructured{u})
}

func setPSA(labels map[string]string, mode string, level quartersv1alpha1.PodSecurityLevel) {
	if level == "" {
		return
	}
	labels["pod-security.kubernetes.io/"+mode] = string(level)
	labels["pod-security.kubernetes.io/"+mode+"-version"] = "latest"
}
