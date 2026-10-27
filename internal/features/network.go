package features

import (
	"context"
	"fmt"
	"sort"

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

// RenderNetworkPolicies returns the ingress and egress policies of a namespace,
// followed by the policies of its podPolicies. A nil spec means deny everything.
func RenderNetworkPolicies(ns string, spec *quartersv1alpha1.NetworkSpec) []*unstructured.Unstructured {
	if spec == nil {
		spec = &quartersv1alpha1.NetworkSpec{}
	}
	// Deny wins in Cilium, so the pods a podPolicy allows something to must be
	// left out of the namespace-wide deny of that direction.
	var ingressExempt, egressExempt []quartersv1alpha1.PodSelector
	for _, pp := range spec.PodPolicies {
		if pp.Ingress != nil && len(ingressRules(pp.Ingress)) > 0 {
			ingressExempt = append(ingressExempt, pp.PodSelector)
		}
		if pp.Egress != nil && len(egressRules(pp.Egress)) > 0 {
			egressExempt = append(egressExempt, pp.PodSelector)
		}
	}
	out := []*unstructured.Unstructured{
		ingressPolicy(ns, "", allPods(), spec.Ingress, ingressExempt),
		egressPolicy(ns, "", allPods(), spec.Egress, egressExempt),
	}
	for _, pp := range spec.PodPolicies {
		sel := map[string]any{"matchLabels": toAnyMap(pp.PodSelector.MatchLabels)}
		if pp.Ingress != nil {
			out = append(out, ingressPolicy(ns, pp.Name+"-", sel, pp.Ingress, nil))
		}
		if pp.Egress != nil {
			out = append(out, egressPolicy(ns, pp.Name+"-", sel, pp.Egress, nil))
		}
	}
	return out
}

func allPods() map[string]any { return map[string]any{} }

// withoutPods narrows selector to the pods that carry none of the exempt labels.
func withoutPods(selector map[string]any, exempt []quartersv1alpha1.PodSelector) map[string]any {
	values := map[string][]string{}
	for _, e := range exempt {
		for k, v := range e.MatchLabels {
			values[k] = append(values[k], v)
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var exprs []any
	for _, k := range keys {
		vs := values[k]
		sort.Strings(vs)
		exprs = append(exprs, map[string]any{"key": k, "operator": "NotIn", "values": toAnySlice(vs)})
	}
	out := map[string]any{"matchExpressions": exprs}
	for k, v := range selector {
		out[k] = v
	}
	return out
}

func ingressRules(in *quartersv1alpha1.IngressSpec) []any {
	var rules []any
	if in == nil {
		return nil
	}
	if ptr.Deref(in.AllowSameNamespace, false) {
		rules = append(rules, map[string]any{"fromEndpoints": []any{map[string]any{}}})
	}
	if ptr.Deref(in.AllowCiliumIngress, false) {
		rules = append(rules, map[string]any{"fromEntities": []any{"ingress"}})
	}
	for _, other := range in.AllowFromNamespaces {
		rules = append(rules, map[string]any{"fromEndpoints": []any{namespaceSelector(other)}})
	}
	return rules
}

// ingressPolicy renders the allow or deny ingress policy of the pods selected
// by selector. The pods in exempt are left out of a deny policy.
func ingressPolicy(ns, prefix string, selector map[string]any, in *quartersv1alpha1.IngressSpec, exempt []quartersv1alpha1.PodSelector) *unstructured.Unstructured {
	rules := ingressRules(in)
	if len(rules) == 0 {
		if len(exempt) > 0 {
			selector = withoutPods(selector, exempt)
		}
		return policy(ns, prefix+"deny-ingress", selector, map[string]any{
			"ingressDeny": []any{map[string]any{"fromEntities": []any{"all"}}},
		})
	}
	return policy(ns, prefix+"allow-ingress", selector, map[string]any{"ingress": rules})
}

func egressRules(eg *quartersv1alpha1.EgressSpec) []any {
	var rules []any
	if eg == nil {
		return nil
	}
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
	return rules
}

// egressPolicy renders the allow or deny egress policy of the pods selected by
// selector. The pods in exempt are left out of a deny policy.
func egressPolicy(ns, prefix string, selector map[string]any, eg *quartersv1alpha1.EgressSpec, exempt []quartersv1alpha1.PodSelector) *unstructured.Unstructured {
	rules := egressRules(eg)
	if len(rules) == 0 {
		if len(exempt) > 0 {
			selector = withoutPods(selector, exempt)
		}
		return policy(ns, prefix+"deny-egress", selector, map[string]any{
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
	return policy(ns, prefix+"allow-egress", selector, map[string]any{"egress": append([]any{dns}, rules...)})
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

func policy(ns, suffix string, selector, spec map[string]any) *unstructured.Unstructured {
	spec["endpointSelector"] = selector
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	u.SetGroupVersionKind(ciliumPolicyGVK)
	u.SetName(ns + "-" + suffix)
	u.SetNamespace(ns)
	return u
}

func toAnyMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
