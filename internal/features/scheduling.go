package features

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

var (
	assignGVK     = schema.GroupVersionKind{Group: "mutations.gatekeeper.sh", Version: "v1", Kind: "Assign"}
	constraintGVK = schema.GroupVersionKind{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Kind: "QuartersScheduling"}
)

// Scheduling places the pods of a namespace using OPA Gatekeeper.
//
//   - Force: one Assign mutation per node label / toleration injects the value
//     into every pod, overriding what the pod asked for.
//   - Allow: one Constraint of the generic QuartersScheduling template rejects
//     pods that use a value outside the allowed set.
//
// Node selectors and tolerations have independent modes. Gatekeeper must be
// installed with its mutation feature; the ConstraintTemplate is shipped in
// config/gatekeeper.
type Scheduling struct{}

func (Scheduling) Name() string   { return "Scheduling" }
func (Scheduling) Critical() bool { return false }
func (Scheduling) Kinds() []schema.GroupVersionKind {
	return []schema.GroupVersionKind{assignGVK, constraintGVK}
}

func (s Scheduling) Reconcile(ctx context.Context, a *Applier, in Input) error {
	desired, err := RenderScheduling(in.Namespace(), in.Config.Scheduling)
	if err != nil {
		return err
	}
	return a.Apply(ctx, in.Tenant, "scheduling", s.Kinds(), desired)
}

// RenderScheduling returns the Gatekeeper objects for a namespace.
func RenderScheduling(ns string, spec *quartersv1alpha1.SchedulingSpec) ([]*unstructured.Unstructured, error) {
	if spec == nil {
		return nil, nil
	}
	var out []*unstructured.Unstructured
	params := map[string]any{}

	if sel := spec.NodeSelector; sel != nil && len(sel.Labels) > 0 {
		switch sel.Mode {
		case quartersv1alpha1.SchedulingForce:
			for _, k := range sortedKeys(sel.Labels) {
				out = append(out, assign(ns, "ns", k, "spec.nodeSelector."+quote(k), sel.Labels[k]))
			}
		case quartersv1alpha1.SchedulingAllow:
			labels := map[string]any{}
			for k, v := range sel.Labels {
				labels[k] = v
			}
			params["checkNodeSelector"] = true
			params["nodeSelector"] = labels
		}
	}

	if tol := spec.Tolerations; tol != nil {
		switch tol.Mode {
		case quartersv1alpha1.SchedulingForce:
			for _, item := range tol.Items {
				if item.Key == "" {
					return nil, fmt.Errorf("toleration without key cannot be forced")
				}
				value, err := tolerationMap(item)
				if err != nil {
					return nil, err
				}
				out = append(out, assign(ns, "tol", item.Key+"/"+string(item.Effect), "spec.tolerations[key: "+quote(item.Key)+"]", value))
			}
		case quartersv1alpha1.SchedulingAllow:
			var items []any
			for _, item := range tol.Items {
				m, err := tolerationMap(item)
				if err != nil {
					return nil, err
				}
				items = append(items, m)
			}
			params["checkTolerations"] = true
			params["tolerations"] = items
		}
	}

	if len(params) > 0 {
		c := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
			"match": map[string]any{
				"kinds":      []any{map[string]any{"apiGroups": []any{""}, "kinds": []any{"Pod"}}},
				"namespaces": []any{ns},
			},
			"parameters": params,
		}}}
		c.SetGroupVersionKind(constraintGVK)
		c.SetName("quarters-" + ns)
		out = append(out, c)
	}
	return out, nil
}

func assign(ns, kind, id, location string, value any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		"applyTo": []any{map[string]any{"groups": []any{""}, "kinds": []any{"Pod"}, "versions": []any{"v1"}}},
		"match": map[string]any{
			"scope":      "Namespaced",
			"kinds":      []any{map[string]any{"apiGroups": []any{"*"}, "kinds": []any{"Pod"}}},
			"namespaces": []any{ns},
		},
		"location":   location,
		"parameters": map[string]any{"assign": map[string]any{"value": value}},
	}}}
	u.SetGroupVersionKind(assignGVK)
	u.SetName(fmt.Sprintf("quarters-%s-%s-%s", ns, kind, shortHash(id)))
	return u
}

// tolerationMap turns a Toleration into plain JSON data.
func tolerationMap(t corev1.Toleration) (map[string]any, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// quote makes a key usable as one element of a Gatekeeper location path.
func quote(s string) string { return `"` + s + `"` }

func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
