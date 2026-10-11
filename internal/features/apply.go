package features

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

// ManagedByLabel and ManagedByValue mark everything created by Quarters.
const (
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "quarters"
	fieldOwnerBase = "quarters/"
)

var tenantGVK = quartersv1alpha1.GroupVersion.WithKind("Tenant")

// helmAnnotations are removed from adopted objects so Helm releases them.
var helmAnnotations = []string{"meta.helm.sh/release-name", "meta.helm.sh/release-namespace"}

// Applier applies desired objects with server-side apply, so manual edits of
// managed fields are reverted and other controllers' fields are left alone.
type Applier struct {
	Client client.Client
}

// Apply creates or updates every desired object, then deletes objects of the
// given kinds that carry this tenant and module label but are not desired.
func (a *Applier) Apply(ctx context.Context, tenant *quartersv1alpha1.Tenant, module string, kinds []schema.GroupVersionKind, desired []*unstructured.Unstructured) error {
	adopt := tenant.Annotations[quartersv1alpha1.AdoptAnnotation] == "true"
	keep := map[string]struct{}{}

	for _, obj := range desired {
		obj = obj.DeepCopy()
		a.decorate(obj, tenant, module)
		if err := a.Client.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldOwnerBase+module), client.ForceOwnership); err != nil {
			return fmt.Errorf("apply %s %s: %w", obj.GetKind(), key(obj), err)
		}
		if adopt {
			if err := a.releaseFromHelm(ctx, obj); err != nil {
				return err
			}
		}
		keep[objectID(obj)] = struct{}{}
	}

	for _, gvk := range kinds {
		list, err := a.listManaged(ctx, gvk, tenant.Name, module)
		if err != nil {
			return err
		}
		for i := range list {
			if _, ok := keep[objectID(&list[i])]; ok {
				continue
			}
			if err := a.Client.Delete(ctx, &list[i]); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("prune %s %s: %w", gvk.Kind, key(&list[i]), err)
			}
		}
	}
	return nil
}

// Orphan removes the owner reference to the Tenant from every object it
// created, so they survive the deletion of the Tenant (deletionPolicy Retain).
func (a *Applier) Orphan(ctx context.Context, tenant *quartersv1alpha1.Tenant, kinds []schema.GroupVersionKind) error {
	for _, gvk := range kinds {
		list, err := a.listManaged(ctx, gvk, tenant.Name, "")
		if err != nil {
			return err
		}
		for i := range list {
			obj := &list[i]
			owners := obj.GetOwnerReferences()
			kept := owners[:0:0]
			for _, o := range owners {
				if o.UID != tenant.UID {
					kept = append(kept, o)
				}
			}
			if len(kept) == len(owners) {
				continue
			}
			patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"ownerReferences": kept}})
			if err := a.Client.Patch(ctx, obj, client.RawPatch(types.MergePatchType, patch)); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("orphan %s %s: %w", gvk.Kind, key(obj), err)
			}
		}
	}
	return nil
}

func (a *Applier) decorate(obj *unstructured.Unstructured, tenant *quartersv1alpha1.Tenant, module string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[ManagedByLabel] = ManagedByValue
	labels[quartersv1alpha1.TenantLabel] = tenant.Name
	labels[quartersv1alpha1.ModuleLabel] = module
	obj.SetLabels(labels)

	// The namespace is governed by the finalizer and deletionPolicy instead.
	if obj.GetKind() != "Namespace" {
		obj.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion:         tenantGVK.GroupVersion().String(),
			Kind:               tenantGVK.Kind,
			Name:               tenant.Name,
			UID:                tenant.UID,
			Controller:         ptr.To(true),
			BlockOwnerDeletion: ptr.To(false),
		}})
	}
	// Never carry server populated fields into an apply request.
	unstructured.RemoveNestedField(obj.Object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(obj.Object, "status")
}

func (a *Applier) releaseFromHelm(ctx context.Context, obj *unstructured.Unstructured) error {
	annotations := map[string]any{}
	for _, k := range helmAnnotations {
		annotations[k] = nil
	}
	patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": annotations}})
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(obj.GroupVersionKind())
	cur.SetNamespace(obj.GetNamespace())
	cur.SetName(obj.GetName())
	if err := a.Client.Patch(ctx, cur, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("release %s %s from helm: %w", obj.GetKind(), key(obj), err)
	}
	return nil
}

// listManaged lists objects of a kind created for a tenant (and module, if set).
// A kind whose CRD is not installed has no objects.
func (a *Applier) listManaged(ctx context.Context, gvk schema.GroupVersionKind, tenant, module string) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	sel := client.MatchingLabels{ManagedByLabel: ManagedByValue, quartersv1alpha1.TenantLabel: tenant}
	if module != "" {
		sel[quartersv1alpha1.ModuleLabel] = module
	}
	if err := a.Client.List(ctx, list, sel); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) || runtime.IsNotRegisteredError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list %s: %w", gvk.Kind, err)
	}
	return list.Items, nil
}

func objectID(o *unstructured.Unstructured) string {
	return o.GetKind() + "/" + key(o)
}

func key(o *unstructured.Unstructured) string {
	if o.GetNamespace() == "" {
		return o.GetName()
	}
	return o.GetNamespace() + "/" + o.GetName()
}

// toUnstructured converts a typed object, which must have its TypeMeta set.
func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}
