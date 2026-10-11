package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

var (
	cnpGVK    = schema.GroupVersionKind{Group: "cilium.io", Version: "v2", Kind: "CiliumNetworkPolicy"}
	assignGVK = schema.GroupVersionKind{Group: "mutations.gatekeeper.sh", Version: "v1", Kind: "Assign"}
)

func q(s string) resource.Quantity { return resource.MustParse(s) }

var _ = Describe("Tenant controller", func() {
	var r *TenantReconciler

	BeforeEach(func() {
		r = &TenantReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(1000)}
	})

	reconcile := func(name string) error {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: name}})
		return err
	}
	get := func(name string) *quartersv1alpha1.Tenant {
		t := &quartersv1alpha1.Tenant{}
		ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKey{Name: name}, t)).To(Succeed())
		return t
	}
	newProfile := func(name string, cfg quartersv1alpha1.TenantConfig) {
		p := &quartersv1alpha1.TenantProfile{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: quartersv1alpha1.TenantProfileSpec{TenantConfig: cfg}}
		ExpectWithOffset(1, client.IgnoreAlreadyExists(k8sClient.Create(ctx, p))).To(Succeed())
	}
	newTenant := func(name, profile string, mod func(*quartersv1alpha1.Tenant)) *quartersv1alpha1.Tenant {
		t := &quartersv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       quartersv1alpha1.TenantSpec{ProfileRef: profile, Team: "backend", DeletionPolicy: quartersv1alpha1.DeletionRetain},
		}
		if mod != nil {
			mod(t)
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, t)).To(Succeed())
		return t
	}
	cnp := func(ns, name string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(cnpGVK)
		return u
	}
	cnpExists := func(ns, name string) bool {
		return k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, cnp(ns, name)) == nil
	}
	condition := func(t *quartersv1alpha1.Tenant, typ string) *metav1.Condition {
		return meta.FindStatusCondition(t.Status.Conditions, typ)
	}

	It("creates everything a Helm release used to create, from profile plus overrides", func() {
		newProfile("std", quartersv1alpha1.TenantConfig{
			Quota:      map[string]resource.Quantity{"pods": q("20"), "requests.cpu": q("2")},
			LimitRange: &quartersv1alpha1.LimitRangeSpec{Default: corev1.ResourceList{"cpu": q("100m")}, DefaultRequest: corev1.ResourceList{"cpu": q("100m")}},
			PodSecurity: &quartersv1alpha1.PodSecuritySpec{
				Enforce: "baseline", Audit: "restricted", Warn: "restricted",
			},
			Network: &quartersv1alpha1.NetworkSpec{Ingress: &quartersv1alpha1.IngressSpec{AllowSameNamespace: ptr.To(true)}},
		})
		newTenant("t-full", "std", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Labels = map[string]string{"environment": "production"}
			t.Spec.Quota = map[string]resource.Quantity{"pods": q("50")}
		})

		Expect(reconcile("t-full")).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "t-full"}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue("team", "backend"))
		Expect(ns.Labels).To(HaveKeyWithValue("environment", "production"))
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "baseline"))
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/warn", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "quarters"))

		quota := &corev1.ResourceQuota{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "t-full", Name: "t-full-quota"}, quota)).To(Succeed())
		pods := quota.Spec.Hard["pods"]
		Expect(pods.String()).To(Equal("50"), "tenant overrides the profile")
		cpu := quota.Spec.Hard["requests.cpu"]
		Expect(cpu.String()).To(Equal("2"), "tenant inherits what it does not override")
		Expect(quota.OwnerReferences).To(HaveLen(1))

		lr := &corev1.LimitRange{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "t-full", Name: "t-full-quota-limit-range"}, lr)).To(Succeed())

		Expect(cnpExists("t-full", "t-full-allow-ingress")).To(BeTrue())
		Expect(cnpExists("t-full", "t-full-deny-egress")).To(BeTrue(), "nothing allowed means deny")
		Expect(cnpExists("t-full", "t-full-deny-ingress")).To(BeFalse())

		t := get("t-full")
		Expect(t.Status.Phase).To(Equal("Ready"))
		Expect(condition(t, "Ready").Status).To(Equal(metav1.ConditionTrue))
		for _, c := range []string{"NamespaceReady", "QuotaReady", "LimitRangeReady", "NetworkReady", "SchedulingReady"} {
			Expect(condition(t, c)).NotTo(BeNil(), c)
			Expect(condition(t, c).Status).To(Equal(metav1.ConditionTrue), c)
		}
		Expect(t.Status.Resolved).NotTo(BeNil())
		Expect(t.Finalizers).To(ContainElement(quartersv1alpha1.Finalizer))
	})

	It("reverts manual changes to managed fields", func() {
		newProfile("std-drift", quartersv1alpha1.TenantConfig{Quota: map[string]resource.Quantity{"pods": q("20")}})
		newTenant("t-drift", "std-drift", nil)
		Expect(reconcile("t-drift")).To(Succeed())

		quota := &corev1.ResourceQuota{}
		key := client.ObjectKey{Namespace: "t-drift", Name: "t-drift-quota"}
		Expect(k8sClient.Get(ctx, key, quota)).To(Succeed())
		quota.Spec.Hard["pods"] = q("9999")
		Expect(k8sClient.Update(ctx, quota)).To(Succeed())

		Expect(reconcile("t-drift")).To(Succeed())
		Expect(k8sClient.Get(ctx, key, quota)).To(Succeed())
		pods := quota.Spec.Hard["pods"]
		Expect(pods.String()).To(Equal("20"))
	})

	It("reports a missing profile and creates nothing", func() {
		newTenant("t-noprofile", "does-not-exist", nil)
		Expect(reconcile("t-noprofile")).To(Succeed(), "waiting for the profile is not an error to retry")

		t := get("t-noprofile")
		Expect(t.Status.Phase).To(Equal("Degraded"))
		Expect(condition(t, "ProfileResolved").Status).To(Equal(metav1.ConditionFalse))
		err := k8sClient.Get(ctx, client.ObjectKey{Name: "t-noprofile"}, &corev1.Namespace{})
		Expect(err).To(HaveOccurred())
	})

	It("prunes the allow policy when the tenant goes back to deny, never keeping both", func() {
		newProfile("std-net", quartersv1alpha1.TenantConfig{})
		newTenant("t-net", "std-net", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{Ingress: &quartersv1alpha1.IngressSpec{AllowSameNamespace: ptr.To(true)}}
		})
		Expect(reconcile("t-net")).To(Succeed())
		Expect(cnpExists("t-net", "t-net-allow-ingress")).To(BeTrue())

		t := get("t-net")
		t.Spec.Network = nil
		Expect(k8sClient.Update(ctx, t)).To(Succeed())
		Expect(reconcile("t-net")).To(Succeed())

		Expect(cnpExists("t-net", "t-net-allow-ingress")).To(BeFalse())
		Expect(cnpExists("t-net", "t-net-deny-ingress")).To(BeTrue())
	})

	It("refuses to take over an existing namespace unless asked, and releases it from Helm when adopting", func() {
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:        "t-adopt",
			Labels:      map[string]string{"app.kubernetes.io/managed-by": "Helm"},
			Annotations: map[string]string{"meta.helm.sh/release-name": "ns-config", "meta.helm.sh/release-namespace": "default", "keep": "me"},
		}})).To(Succeed())
		newProfile("std-adopt", quartersv1alpha1.TenantConfig{})
		newTenant("t-adopt", "std-adopt", nil)

		Expect(reconcile("t-adopt")).NotTo(Succeed())
		t := get("t-adopt")
		Expect(condition(t, "NamespaceReady").Status).To(Equal(metav1.ConditionFalse))
		Expect(condition(t, "NamespaceReady").Message).To(ContainSubstring("adopt"))
		Expect(condition(t, "QuotaReady").Status).To(Equal(metav1.ConditionUnknown), "later modules wait for the namespace")

		t.Annotations = map[string]string{quartersv1alpha1.AdoptAnnotation: "true"}
		Expect(k8sClient.Update(ctx, t)).To(Succeed())
		Expect(reconcile("t-adopt")).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "t-adopt"}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "quarters"))
		Expect(ns.Annotations).NotTo(HaveKey("meta.helm.sh/release-name"))
		Expect(ns.Annotations).To(HaveKeyWithValue("keep", "me"), "unrelated annotations are untouched")
	})

	It("forces node placement with Gatekeeper mutations and removes them when disabled", func() {
		newProfile("std-sched", quartersv1alpha1.TenantConfig{})
		newTenant("t-sched", "std-sched", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Scheduling = &quartersv1alpha1.SchedulingSpec{
				NodeSelector: &quartersv1alpha1.NodeSelectorSpec{Mode: quartersv1alpha1.SchedulingForce, Labels: map[string]string{"location": "site-a"}},
			}
		})
		Expect(reconcile("t-sched")).To(Succeed())

		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(assignGVK.GroupVersion().WithKind("AssignList"))
		Expect(k8sClient.List(ctx, list, client.MatchingLabels{quartersv1alpha1.TenantLabel: "t-sched"})).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].GetOwnerReferences()).To(HaveLen(1))

		t := get("t-sched")
		t.Spec.Scheduling.NodeSelector.Mode = quartersv1alpha1.SchedulingDisabled
		Expect(k8sClient.Update(ctx, t)).To(Succeed())
		Expect(reconcile("t-sched")).To(Succeed())
		Expect(k8sClient.List(ctx, list, client.MatchingLabels{quartersv1alpha1.TenantLabel: "t-sched"})).To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})

	It("keeps everything on delete with policy Retain", func() {
		newProfile("std-retain", quartersv1alpha1.TenantConfig{Quota: map[string]resource.Quantity{"pods": q("5")}})
		newTenant("t-retain", "std-retain", nil)
		Expect(reconcile("t-retain")).To(Succeed())

		Expect(k8sClient.Delete(ctx, get("t-retain"))).To(Succeed())
		Expect(reconcile("t-retain")).To(Succeed())

		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "t-retain"}, &quartersv1alpha1.Tenant{})).NotTo(Succeed(), "finalizer released")
		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "t-retain"}, ns)).To(Succeed())
		Expect(ns.DeletionTimestamp.IsZero()).To(BeTrue())
		quota := &corev1.ResourceQuota{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "t-retain", Name: "t-retain-quota"}, quota)).To(Succeed())
		Expect(quota.OwnerReferences).To(BeEmpty(), "orphaned so garbage collection keeps it")
	})

	It("deletes the namespace on delete with policy Delete, but never one it does not manage", func() {
		newProfile("std-del", quartersv1alpha1.TenantConfig{})
		newTenant("t-del", "std-del", func(t *quartersv1alpha1.Tenant) { t.Spec.DeletionPolicy = quartersv1alpha1.DeletionDelete })
		Expect(reconcile("t-del")).To(Succeed())
		Expect(k8sClient.Delete(ctx, get("t-del"))).To(Succeed())
		Expect(reconcile("t-del")).To(Succeed())

		ns := &corev1.Namespace{}
		err := k8sClient.Get(ctx, client.ObjectKey{Name: "t-del"}, ns)
		// envtest has no namespace controller: the namespace stays Terminating.
		Expect(err == nil && !ns.DeletionTimestamp.IsZero() || err != nil).To(BeTrue())

		// A Tenant whose namespace was never taken over must not delete it.
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "t-foreign"}})).To(Succeed())
		newTenant("t-foreign", "std-del", func(t *quartersv1alpha1.Tenant) { t.Spec.DeletionPolicy = quartersv1alpha1.DeletionDelete })
		Expect(reconcile("t-foreign")).NotTo(Succeed())
		Expect(k8sClient.Delete(ctx, get("t-foreign"))).To(Succeed())
		Expect(reconcile("t-foreign")).To(Succeed())
		foreign := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "t-foreign"}, foreign)).To(Succeed())
		Expect(foreign.DeletionTimestamp.IsZero()).To(BeTrue())
	})
})
