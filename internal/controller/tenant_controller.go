package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
	"github.com/pinginfo/quarters/internal/features"
	"github.com/pinginfo/quarters/internal/metrics"
	"github.com/pinginfo/quarters/internal/resolve"
)

const (
	defaultProfile = "default"
	// resyncPeriod is a safety net for kinds that are not watched, for example
	// when a CRD is installed after the operator started.
	resyncPeriod = 10 * time.Minute

	phaseReady       = "Ready"
	phaseDegraded    = "Degraded"
	phaseTerminating = "Terminating"

	condReady   = "Ready"
	condProfile = "ProfileResolved"
)

// TenantReconciler reconciles a Tenant: it resolves the profile, then runs
// every feature module in order and reports one condition per module.
type TenantReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// Features are the modules to run. Defaults to features.All().
	Features []features.Feature
}

// +kubebuilder:rbac:groups=quarters.pinginfo.io,resources=tenants,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=quarters.pinginfo.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=quarters.pinginfo.io,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups=quarters.pinginfo.io,resources=tenantprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas;limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cilium.io,resources=ciliumnetworkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mutations.gatekeeper.sh,resources=assign,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=constraints.gatekeeper.sh,resources=quartersscheduling,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *TenantReconciler) modules() []features.Feature {
	if r.Features != nil {
		return r.Features
	}
	return features.All()
}

// Reconcile brings the namespace of one Tenant to its desired state.
func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	tenant := &quartersv1alpha1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tenant.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, tenant)
	}

	if controllerutil.AddFinalizer(tenant, quartersv1alpha1.Finalizer) {
		if err := r.Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
	}

	status := tenant.Status.DeepCopy()
	status.ObservedGeneration = tenant.Generation

	resolved, err := r.resolve(ctx, tenant)
	if err != nil {
		setCondition(status, condProfile, metav1.ConditionFalse, "ProfileNotResolved", err.Error(), tenant.Generation)
		setCondition(status, condReady, metav1.ConditionFalse, "ProfileNotResolved", err.Error(), tenant.Generation)
		status.Phase = phaseDegraded
		r.event(tenant, corev1.EventTypeWarning, "ProfileNotResolved", err.Error())
		return ctrl.Result{}, errors.Join(r.patchStatus(ctx, tenant, status), ignoreMissingProfile(err))
	}
	setCondition(status, condProfile, metav1.ConditionTrue, "Resolved", "profile merged", tenant.Generation)
	status.Resolved = &resolved

	applier := &features.Applier{Client: r.Client}
	input := features.Input{Tenant: tenant, Config: resolved}
	failed := r.runModules(ctx, applier, input, status)

	if len(failed) == 0 {
		setCondition(status, condReady, metav1.ConditionTrue, "AllModulesReady", "all feature modules are applied", tenant.Generation)
		status.Phase = phaseReady
	} else {
		msg := fmt.Sprintf("failing modules: %v", failed)
		setCondition(status, condReady, metav1.ConditionFalse, "ModuleFailed", msg, tenant.Generation)
		status.Phase = phaseDegraded
	}

	if err := r.patchStatus(ctx, tenant, status); err != nil {
		return ctrl.Result{}, err
	}
	if len(failed) > 0 {
		log.Info("tenant degraded", "failed", failed)
		return ctrl.Result{}, fmt.Errorf("modules failed: %v", failed)
	}
	return ctrl.Result{RequeueAfter: resyncPeriod}, nil
}

// runModules runs the modules in order and records one condition each. After a
// failed critical module the remaining ones are skipped.
func (r *TenantReconciler) runModules(ctx context.Context, a *features.Applier, in features.Input, status *quartersv1alpha1.TenantStatus) []string {
	log := logf.FromContext(ctx)
	var failed []string
	skip := ""

	for _, m := range r.modules() {
		condType := m.Name() + "Ready"
		if skip != "" {
			setCondition(status, condType, metav1.ConditionUnknown, "Skipped", "waiting for module "+skip, in.Tenant.Generation)
			continue
		}

		start := time.Now()
		err := m.Reconcile(ctx, a, in)
		metrics.ModuleDuration.WithLabelValues(m.Name()).Observe(time.Since(start).Seconds())

		if err != nil {
			metrics.ModuleErrors.WithLabelValues(m.Name()).Inc()
			log.Error(err, "module failed", "module", m.Name())
			setCondition(status, condType, metav1.ConditionFalse, "ReconcileFailed", err.Error(), in.Tenant.Generation)
			r.event(in.Tenant, corev1.EventTypeWarning, m.Name()+"Failed", err.Error())
			failed = append(failed, m.Name())
			if m.Critical() {
				skip = m.Name()
			}
			continue
		}
		setCondition(status, condType, metav1.ConditionTrue, "Applied", "applied", in.Tenant.Generation)
	}
	return failed
}

// resolve loads the profile and merges the Tenant overrides over it.
func (r *TenantReconciler) resolve(ctx context.Context, t *quartersv1alpha1.Tenant) (quartersv1alpha1.TenantConfig, error) {
	name := t.Spec.ProfileRef
	if name == "" {
		name = defaultProfile
	}
	profile := &quartersv1alpha1.TenantProfile{}
	if err := r.Get(ctx, client.ObjectKey{Name: name}, profile); err != nil {
		if apierrors.IsNotFound(err) {
			return quartersv1alpha1.TenantConfig{}, &missingProfileError{name: name}
		}
		return quartersv1alpha1.TenantConfig{}, err
	}
	return resolve.Merge(&profile.Spec.TenantConfig, t.Spec.TenantConfig)
}

// missingProfileError is recorded in the status but not retried: the profile
// watch triggers a new reconcile when the profile appears.
type missingProfileError struct{ name string }

func (e *missingProfileError) Error() string {
	return fmt.Sprintf("TenantProfile %q not found", e.name)
}

func ignoreMissingProfile(err error) error {
	var m *missingProfileError
	if errors.As(err, &m) {
		return nil
	}
	return err
}

// finalize applies the deletion policy and releases the Tenant.
func (r *TenantReconciler) finalize(ctx context.Context, t *quartersv1alpha1.Tenant) error {
	if !controllerutil.ContainsFinalizer(t, quartersv1alpha1.Finalizer) {
		return nil
	}
	if t.Status.Phase != phaseTerminating {
		status := t.Status.DeepCopy()
		status.Phase = phaseTerminating
		if err := r.patchStatus(ctx, t, status); err != nil {
			return err
		}
	}

	if t.Spec.DeletionPolicy == quartersv1alpha1.DeletionDelete {
		if err := r.deleteNamespace(ctx, t); err != nil {
			return err
		}
		r.event(t, corev1.EventTypeNormal, "NamespaceDeleted", "namespace deleted by deletionPolicy Delete")
	} else {
		var kinds []schema.GroupVersionKind
		for _, m := range r.modules() {
			kinds = append(kinds, m.Kinds()...)
		}
		if err := (&features.Applier{Client: r.Client}).Orphan(ctx, t, kinds); err != nil {
			return err
		}
		r.event(t, corev1.EventTypeNormal, "NamespaceRetained", "namespace and its objects kept by deletionPolicy Retain")
	}

	controllerutil.RemoveFinalizer(t, quartersv1alpha1.Finalizer)
	return r.Update(ctx, t)
}

// deleteNamespace deletes the namespace, but only if Quarters created it for
// this Tenant.
func (r *TenantReconciler) deleteNamespace(ctx context.Context, t *quartersv1alpha1.Tenant) error {
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, client.ObjectKey{Name: t.Name}, ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	if ns.Labels[features.ManagedByLabel] != features.ManagedByValue || ns.Labels[quartersv1alpha1.TenantLabel] != t.Name {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, ns))
}

func (r *TenantReconciler) patchStatus(ctx context.Context, t *quartersv1alpha1.Tenant, status *quartersv1alpha1.TenantStatus) error {
	if equalStatus(&t.Status, status) {
		return nil
	}
	base := t.DeepCopy()
	t.Status = *status
	return r.Status().Patch(ctx, t, client.MergeFrom(base))
}

func equalStatus(a, b *quartersv1alpha1.TenantStatus) bool {
	return a.Phase == b.Phase && a.ObservedGeneration == b.ObservedGeneration &&
		runtimeEqual(a.Resolved, b.Resolved) && conditionsEqual(a.Conditions, b.Conditions)
}

func (r *TenantReconciler) event(t *quartersv1alpha1.Tenant, kind, reason, msg string) {
	if r.Recorder != nil {
		r.Recorder.Event(t, kind, reason, msg)
	}
}

func setCondition(status *quartersv1alpha1.TenantStatus, condType string, s metav1.ConditionStatus, reason, msg string, gen int64) {
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: condType, Status: s, Reason: reason, Message: msg, ObservedGeneration: gen,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&quartersv1alpha1.Tenant{}).
		Owns(&corev1.ResourceQuota{}).
		Owns(&corev1.LimitRange{}).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(namespaceToTenant)).
		Watches(&quartersv1alpha1.TenantProfile{}, handler.EnqueueRequestsFromMapFunc(r.profileToTenants)).
		Named("tenant")

	// Cilium and Gatekeeper are optional at startup. Kinds that are installed
	// later are picked up by the periodic resync.
	for _, m := range r.modules() {
		for _, gvk := range m.Kinds() {
			if gvk.Group == "" {
				continue
			}
			if _, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
				logf.Log.WithName("setup").Info("CRD not installed, not watching", "kind", gvk.String())
				continue
			}
			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(gvk)
			b = b.Owns(u)
		}
	}
	return b.Complete(r)
}

func namespaceToTenant(_ context.Context, obj client.Object) []reconcile.Request {
	if name := obj.GetLabels()[quartersv1alpha1.TenantLabel]; name != "" {
		return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name}}}
	}
	// An unmanaged namespace with the name of a Tenant may be waiting for adoption.
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: obj.GetName()}}}
}

func (r *TenantReconciler) profileToTenants(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &quartersv1alpha1.TenantList{}
	if err := r.List(ctx, list); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for _, t := range list.Items {
		name := t.Spec.ProfileRef
		if name == "" {
			name = defaultProfile
		}
		if name == obj.GetName() {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKey{Name: t.Name}})
		}
	}
	return reqs
}
