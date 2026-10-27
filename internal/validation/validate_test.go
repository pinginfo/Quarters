package validation

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

func tenant(name string, mod func(*quartersv1alpha1.Tenant)) *quartersv1alpha1.Tenant {
	t := &quartersv1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: quartersv1alpha1.TenantSpec{Team: "backend"}}
	if mod != nil {
		mod(t)
	}
	return t
}

func TestTenantRules(t *testing.T) {
	statefulsets := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(statefulsets, meta.RESTScopeNamespace)

	cases := []struct {
		name string
		t    *quartersv1alpha1.Tenant
		want string // substring of the expected error, "" for valid
	}{
		{"valid", tenant("app-production", nil), ""},
		{"reserved prefix", tenant("kube-foo", nil), "reserved"},
		{"reserved default", tenant("default", nil), "reserved"},
		{"bad dns name", tenant("Bad_Name", nil), "name"},
		{"missing team", tenant("a", func(t *quartersv1alpha1.Tenant) { t.Spec.Team = " " }), "team"},
		{"reserved label", tenant("a", func(t *quartersv1alpha1.Tenant) { t.Spec.Labels = map[string]string{"team": "x"} }), "managed by the operator"},
		{"psa label", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Labels = map[string]string{"pod-security.kubernetes.io/enforce": "privileged"}
		}), "managed by the operator"},
		{"negative quota", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Quota = map[string]resource.Quantity{"pods": resource.MustParse("-1")}
		}), "negative"},
		{"unknown quota key", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Quota = map[string]resource.Quantity{"pod": resource.MustParse("1")}
		}), "unknown quota"},
		{"quota typo caught by discovery", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Quota = map[string]resource.Quantity{"count/statefullstates.apps": resource.MustParse("5")}
		}), "does not know"},
		{"quota count ok", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Quota = map[string]resource.Quantity{"count/statefulsets.apps": resource.MustParse("5")}
		}), ""},
		{"bad cidr", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{Egress: &quartersv1alpha1.EgressSpec{ToCIDRs: []quartersv1alpha1.CIDRRule{{CIDR: "10.0.0.0"}}}}
		}), "CIDR"},
		{"fqdn needs exactly one", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{Egress: &quartersv1alpha1.EgressSpec{ToFQDNs: []quartersv1alpha1.FQDNRule{{}}}}
		}), "exactly one"},
		{"wildcard in matchName", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{Egress: &quartersv1alpha1.EgressSpec{ToFQDNs: []quartersv1alpha1.FQDNRule{{MatchName: "*"}}}}
		}), "use matchPattern"},
		{"podPolicy needs exactly one label", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{PodPolicies: []quartersv1alpha1.PodPolicy{{Name: "b", PodSelector: quartersv1alpha1.PodSelector{MatchLabels: map[string]string{"a": "1", "b": "2"}}}}}
		}), "exactly one label"},
		{"podPolicy duplicate name", tenant("a", func(t *quartersv1alpha1.Tenant) {
			pp := quartersv1alpha1.PodPolicy{Name: "b", PodSelector: quartersv1alpha1.PodSelector{MatchLabels: map[string]string{"a": "1"}}}
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{PodPolicies: []quartersv1alpha1.PodPolicy{pp, pp}}
		}), "Duplicate"},
		{"podPolicy validates its egress", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{PodPolicies: []quartersv1alpha1.PodPolicy{{Name: "b",
				PodSelector: quartersv1alpha1.PodSelector{MatchLabels: map[string]string{"a": "1"}},
				Egress:      &quartersv1alpha1.EgressSpec{ToCIDRs: []quartersv1alpha1.CIDRRule{{CIDR: "10.0.0.0"}}}}}}
		}), "CIDR"},
		{"valid podPolicy", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Network = &quartersv1alpha1.NetworkSpec{PodPolicies: []quartersv1alpha1.PodPolicy{{Name: "backup",
				PodSelector: quartersv1alpha1.PodSelector{MatchLabels: map[string]string{"role": "backup"}}}}}
		}), ""},
		{"force selector without labels", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Scheduling = &quartersv1alpha1.SchedulingSpec{NodeSelector: &quartersv1alpha1.NodeSelectorSpec{Mode: quartersv1alpha1.SchedulingForce}}
		}), "at least one label"},
		{"force toleration needs key", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Scheduling = &quartersv1alpha1.SchedulingSpec{Tolerations: &quartersv1alpha1.TolerationsSpec{
				Mode: quartersv1alpha1.SchedulingForce, Items: []corev1.Toleration{{Operator: corev1.TolerationOpExists}}}}
		}), "needs a key"},
		{"valid scheduling", tenant("a", func(t *quartersv1alpha1.Tenant) {
			t.Spec.Scheduling = &quartersv1alpha1.SchedulingSpec{NodeSelector: &quartersv1alpha1.NodeSelectorSpec{
				Mode: quartersv1alpha1.SchedulingForce, Labels: map[string]string{"location": "site-a"}}}
		}), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := Validator{Mapper: mapper}.Tenant(tc.t)
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("want valid, got %v", errs)
				}
				return
			}
			if !strings.Contains(errs.ToAggregate().Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, errs)
			}
		})
	}
}
