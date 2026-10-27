package features

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

var ciliumPolicyGVK = schema.GroupVersionKind{Group: "cilium.io", Version: "v2", Kind: "CiliumNetworkPolicy"}

const namespaceLabel = "io.kubernetes.pod.namespace"

// Network manages the CiliumNetworkPolicies <namespace>-{allow,deny}-{ingress,egress}.
//
// For each direction exactly one policy exists: an allow policy when anything
// is allowed, a deny-all policy otherwise. Cilium deny rules win over allow
// rules, so both must never exist together. Switching direction prunes the
// other policy.
type Network struct{}

func (Network) Name() string                     { return "Network" }
func (Network) Critical() bool                   { return false }
func (Network) Kinds() []schema.GroupVersionKind { return []schema.GroupVersionKind{ciliumPolicyGVK} }

func (n Network) Reconcile(ctx context.Context, a *Applier, in Input) error {
	policies := RenderNetworkPolicies(in.Namespace(), in.Config.Network)
	return a.Apply(ctx, in.Tenant, "network", n.Kinds(), policies)
}

// RenderNetworkPolicies returns the ingress and egress policy of a namespace.
// A nil spec means deny everything.
func RenderNetworkPolicies(ns string, spec *quartersv1alpha1.NetworkSpec) []*unstructured.Unstructured {
	if spec == nil {
		spec = &quartersv1alpha1.NetworkSpec{}
	}
	return []*unstructured.Unstructured{
		ingressPolicy(ns, spec.Ingress),
		egressPolicy(ns, spec.Egress),
	}
}

func ingressPolicy(ns string, in *quartersv1alpha1.IngressSpec) *unstructured.Unstructured {
	var rules []any
	if in != nil {
		if ptr.Deref(in.AllowSameNamespace, false) {
			rules = append(rules, map[string]any{"fromEndpoints": []any{map[string]any{}}})
		}
		if ptr.Deref(in.AllowCiliumIngress, false) {
			rules = append(rules, map[string]any{"fromEntities": []any{"ingress"}})
		}
		for _, other := range in.AllowFromNamespaces {
			rules = append(rules, map[string]any{"fromEndpoints": []any{namespaceSelector(other)}})
		}
	}
	if len(rules) == 0 {
		return policy(ns, "deny-ingress", map[string]any{
			"ingressDeny": []any{map[string]any{"fromEntities": []any{"all"}}},
		})
	}
	return policy(ns, "allow-ingress", map[string]any{"ingress": rules})
}

func egressPolicy(ns string, eg *quartersv1alpha1.EgressSpec) *unstructured.Unstructured {
	var rules []any
	if eg != nil {
		if ptr.Deref(eg.AllowAll, false) {
			rules = append(rules, map[string]any{"toEntities": []any{"all"}})
		}
		for _, other := range eg.ToNamespaces {
			rules = append(rules, map[string]any{"toEndpoints": []any{namespaceSelector(other)}})
		}
		for _, c := range eg.ToCIDRs {
			set := map[string]any{"cidr": c.CIDR}
			if len(c.Except) > 0 {
				set["except"] = toAnySlice(c.Except)
			}
			rules = append(rules, map[string]any{"toCIDRSet": []any{set}})
		}
		for _, f := range eg.ToFQDNs {
			rules = append(rules, fqdnRule(f))
		}
		if ptr.Deref(eg.AllowKubeAPIServer, false) {
			rules = append(rules, map[string]any{"toEntities": []any{"kube-apiserver"}})
		}
	}
	if len(rules) == 0 {
		return policy(ns, "deny-egress", map[string]any{
			"egressDeny": []any{map[string]any{"toEntities": []any{"all"}}},
		})
	}
	dns := map[string]any{
		"toEndpoints": []any{map[string]any{"matchLabels": map[string]any{
			namespaceLabel: "kube-system",
			"k8s-app":      "kube-dns",
		}}},
		"toPorts": []any{map[string]any{
			"ports": []any{
				map[string]any{"port": "53", "protocol": "UDP"},
				map[string]any{"port": "53", "protocol": "TCP"},
			},
			"rules": map[string]any{"dns": []any{map[string]any{"matchPattern": "*"}}},
		}},
	}
	return policy(ns, "allow-egress", map[string]any{"egress": append([]any{dns}, rules...)})
}

func fqdnRule(f quartersv1alpha1.FQDNRule) map[string]any {
	name := map[string]any{}
	if f.MatchName != "" {
		name["matchName"] = f.MatchName
	}
	if f.MatchPattern != "" {
		name["matchPattern"] = f.MatchPattern
	}
	rule := map[string]any{"toFQDNs": []any{name}}
	if len(f.Ports) > 0 {
		var ports []any
		for _, p := range f.Ports {
			proto := p.Protocol
			if proto == "" {
				proto = "TCP"
			}
			ports = append(ports, map[string]any{"port": fmt.Sprint(p.Port), "protocol": proto})
		}
		rule["toPorts"] = []any{map[string]any{"ports": ports}}
	}
	return rule
}

func namespaceSelector(ns string) map[string]any {
	return map[string]any{"matchLabels": map[string]any{namespaceLabel: ns}}
}

func policy(ns, suffix string, spec map[string]any) *unstructured.Unstructured {
	spec["endpointSelector"] = map[string]any{}
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	u.SetGroupVersionKind(ciliumPolicyGVK)
	u.SetName(ns + "-" + suffix)
	u.SetNamespace(ns)
	return u
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
