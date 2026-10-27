package features

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

func byName(t *testing.T, objs []*unstructured.Unstructured, name string) *unstructured.Unstructured {
	t.Helper()
	for _, o := range objs {
		if o.GetName() == name {
			return o
		}
	}
	t.Fatalf("object %q not rendered, got %v", name, names(objs))
	return nil
}

func names(objs []*unstructured.Unstructured) []string {
	var n []string
	for _, o := range objs {
		n = append(n, o.GetName())
	}
	return n
}

func TestNetworkNilSpecDeniesEverything(t *testing.T) {
	got := RenderNetworkPolicies("empty-prod", nil)
	byName(t, got, "empty-prod-deny-ingress")
	byName(t, got, "empty-prod-deny-egress")
	if len(got) != 2 {
		t.Fatalf("want exactly 2 policies, got %v", names(got))
	}
}

func TestNetworkIngressAllowReplacesDeny(t *testing.T) {
	spec := &quartersv1alpha1.NetworkSpec{Ingress: &quartersv1alpha1.IngressSpec{
		AllowSameNamespace:  ptr.To(true),
		AllowCiliumIngress:  ptr.To(true),
		AllowFromNamespaces: []string{"ingress-nginx", "monitoring"},
	}}
	got := RenderNetworkPolicies("app-production", spec)

	p := byName(t, got, "app-production-allow-ingress")
	rules, _, _ := unstructured.NestedSlice(p.Object, "spec", "ingress")
	if len(rules) != 4 {
		t.Fatalf("want 4 ingress rules (same ns, cilium, 2 namespaces), got %d", len(rules))
	}
	for _, o := range got {
		if o.GetName() == "app-production-deny-ingress" {
			t.Fatal("allow and deny ingress must never exist together: deny wins in Cilium")
		}
	}
}

func TestNetworkIngressAllFalseIsDeny(t *testing.T) {
	spec := &quartersv1alpha1.NetworkSpec{Ingress: &quartersv1alpha1.IngressSpec{AllowSameNamespace: ptr.To(false)}}
	byName(t, RenderNetworkPolicies("x", spec), "x-deny-ingress")
}

func TestNetworkEgressMatchesChartFor_app_production(t *testing.T) {
	spec := &quartersv1alpha1.NetworkSpec{Egress: &quartersv1alpha1.EgressSpec{
		ToNamespaces: []string{"kube-system", "shared-services"},
		ToCIDRs:      []quartersv1alpha1.CIDRRule{{CIDR: "0.0.0.0/0", Except: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}}},
		ToFQDNs: []quartersv1alpha1.FQDNRule{
			{MatchName: "api.github.com", Ports: []quartersv1alpha1.PortRule{{Port: 443, Protocol: "TCP"}}},
			{MatchPattern: "*.example.com", Ports: []quartersv1alpha1.PortRule{{Port: 443}}},
		},
	}}
	p := byName(t, RenderNetworkPolicies("app-production", spec), "app-production-allow-egress")
	rules, _, _ := unstructured.NestedSlice(p.Object, "spec", "egress")
	// DNS + 2 namespaces + 1 CIDR + 2 FQDNs, as in the Helm chart.
	if len(rules) != 6 {
		t.Fatalf("want 6 egress rules, got %d", len(rules))
	}
	first := rules[0].(map[string]any)
	if _, ok := first["toPorts"]; !ok {
		t.Error("first rule must be the DNS rule")
	}
	last := rules[5].(map[string]any)
	ports, _, _ := unstructured.NestedSlice(last, "toPorts")
	port := ports[0].(map[string]any)["ports"].([]any)[0].(map[string]any)
	if port["port"] != "443" || port["protocol"] != "TCP" {
		t.Errorf("fqdn port = %v, want 443/TCP (protocol defaults to TCP)", port)
	}
}

func TestNetworkAllowAllEgress(t *testing.T) {
	spec := &quartersv1alpha1.NetworkSpec{Egress: &quartersv1alpha1.EgressSpec{AllowAll: ptr.To(true)}}
	got := RenderNetworkPolicies("x", spec)
	byName(t, got, "x-allow-egress")
}

func TestNetworkEgressAllowKubeAPIServer(t *testing.T) {
	spec := &quartersv1alpha1.NetworkSpec{Egress: &quartersv1alpha1.EgressSpec{AllowKubeAPIServer: ptr.To(true)}}
	p := byName(t, RenderNetworkPolicies("x", spec), "x-allow-egress")
	rules, _, _ := unstructured.NestedSlice(p.Object, "spec", "egress")
	if len(rules) != 2 {
		t.Fatalf("want DNS + 1 entity rule, got %d", len(rules))
	}
	ents, _, _ := unstructured.NestedStringSlice(rules[1].(map[string]any), "toEntities")
	if len(ents) != 1 || ents[0] != "kube-apiserver" {
		t.Errorf("toEntities = %v, want [kube-apiserver]", ents)
	}
}
