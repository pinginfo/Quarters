package resolve

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

func q(s string) resource.Quantity { return resource.MustParse(s) }

func TestMergeQuotaMapsMergeKeyByKey(t *testing.T) {
	profile := &quartersv1alpha1.TenantConfig{Quota: map[string]resource.Quantity{"pods": q("20"), "requests.cpu": q("2")}}
	over := quartersv1alpha1.TenantConfig{Quota: map[string]resource.Quantity{"pods": q("50")}}

	got, err := Merge(profile, over)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(got.Quota["pods"], "50") {
		t.Errorf("pods = %v, want 50", got.Quota["pods"])
	}
	if !eq(got.Quota["requests.cpu"], "2") {
		t.Errorf("requests.cpu = %v, want 2 (inherited)", got.Quota["requests.cpu"])
	}
}

func TestMergeExplicitFalseOverridesTrue(t *testing.T) {
	profile := &quartersv1alpha1.TenantConfig{Network: &quartersv1alpha1.NetworkSpec{
		Ingress: &quartersv1alpha1.IngressSpec{AllowSameNamespace: ptr.To(true), AllowCiliumIngress: ptr.To(true)},
	}}
	over := quartersv1alpha1.TenantConfig{Network: &quartersv1alpha1.NetworkSpec{
		Ingress: &quartersv1alpha1.IngressSpec{AllowSameNamespace: ptr.To(false)},
	}}

	got, err := Merge(profile, over)
	if err != nil {
		t.Fatal(err)
	}
	in := got.Network.Ingress
	if in.AllowSameNamespace == nil || *in.AllowSameNamespace {
		t.Error("allowSameNamespace should be false")
	}
	if in.AllowCiliumIngress == nil || !*in.AllowCiliumIngress {
		t.Error("allowCiliumIngress should be inherited as true")
	}
}

func TestMergeListsReplace(t *testing.T) {
	profile := &quartersv1alpha1.TenantConfig{Network: &quartersv1alpha1.NetworkSpec{
		Egress: &quartersv1alpha1.EgressSpec{ToNamespaces: []string{"kube-system", "shared"}},
	}}
	over := quartersv1alpha1.TenantConfig{Network: &quartersv1alpha1.NetworkSpec{
		Egress: &quartersv1alpha1.EgressSpec{ToNamespaces: []string{"monitoring"}},
	}}

	got, err := Merge(profile, over)
	if err != nil {
		t.Fatal(err)
	}
	if ns := got.Network.Egress.ToNamespaces; len(ns) != 1 || ns[0] != "monitoring" {
		t.Errorf("toNamespaces = %v, want [monitoring]", ns)
	}
}

func TestMergeNilProfileAndInputsUntouched(t *testing.T) {
	over := quartersv1alpha1.TenantConfig{Quota: map[string]resource.Quantity{"pods": q("5")}}
	got, err := Merge(nil, over)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(got.Quota["pods"], "5") {
		t.Errorf("pods = %v", got.Quota["pods"])
	}

	profile := &quartersv1alpha1.TenantConfig{Quota: map[string]resource.Quantity{"pods": q("1")}}
	if _, err := Merge(profile, over); err != nil {
		t.Fatal(err)
	}
	if !eq(profile.Quota["pods"], "1") {
		t.Error("profile was modified")
	}
}

func eq(have resource.Quantity, want string) bool {
	w := resource.MustParse(want)
	return have.Cmp(w) == 0
}
