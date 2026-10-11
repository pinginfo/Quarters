package features

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

func TestSchedulingNilOrDisabledRendersNothing(t *testing.T) {
	for _, spec := range []*quartersv1alpha1.SchedulingSpec{
		nil,
		{},
		{NodeSelector: &quartersv1alpha1.NodeSelectorSpec{Mode: quartersv1alpha1.SchedulingDisabled, Labels: map[string]string{"location": "site-a"}}},
	} {
		got, err := RenderScheduling("ns", spec)
		if err != nil || len(got) != 0 {
			t.Errorf("spec %+v rendered %v, err %v; want nothing", spec, names(got), err)
		}
	}
}

func TestSchedulingForceNodeSelectorIsGenericAndQuoted(t *testing.T) {
	spec := &quartersv1alpha1.SchedulingSpec{NodeSelector: &quartersv1alpha1.NodeSelectorSpec{
		Mode:   quartersv1alpha1.SchedulingForce,
		Labels: map[string]string{"location": "site-a", "topology.kubernetes.io/zone": "z1"},
	}}
	got, err := RenderScheduling("media", spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want one Assign per label, got %v", names(got))
	}
	locations := map[string]any{}
	for _, o := range got {
		if o.GetKind() != "Assign" {
			t.Errorf("kind = %s, want Assign", o.GetKind())
		}
		loc, _, _ := unstructured.NestedString(o.Object, "spec", "location")
		val, _, _ := unstructured.NestedString(o.Object, "spec", "parameters", "assign", "value")
		locations[loc] = val
		ns, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "match", "namespaces")
		if len(ns) != 1 || ns[0] != "media" {
			t.Errorf("assign must be scoped to the namespace, got %v", ns)
		}
	}
	if locations[`spec.nodeSelector."location"`] != "site-a" || locations[`spec.nodeSelector."topology.kubernetes.io/zone"`] != "z1" {
		t.Errorf("unexpected assigns: %v", locations)
	}
}

func TestSchedulingForceTolerationsNeedKey(t *testing.T) {
	spec := &quartersv1alpha1.SchedulingSpec{Tolerations: &quartersv1alpha1.TolerationsSpec{
		Mode:  quartersv1alpha1.SchedulingForce,
		Items: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
	}}
	if _, err := RenderScheduling("ns", spec); err == nil {
		t.Fatal("forcing a toleration without key must fail")
	}
}

func TestSchedulingModesAreIndependent(t *testing.T) {
	spec := &quartersv1alpha1.SchedulingSpec{
		NodeSelector: &quartersv1alpha1.NodeSelectorSpec{Mode: quartersv1alpha1.SchedulingForce, Labels: map[string]string{"location": "site-a"}},
		Tolerations: &quartersv1alpha1.TolerationsSpec{Mode: quartersv1alpha1.SchedulingAllow, Items: []corev1.Toleration{
			{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "site-a", Effect: corev1.TaintEffectNoSchedule},
		}},
	}
	got, err := RenderScheduling("ns", spec)
	if err != nil {
		t.Fatal(err)
	}
	var assigns, constraints int
	for _, o := range got {
		switch o.GetKind() {
		case "Assign":
			assigns++
		case "QuartersScheduling":
			constraints++
			if v, _, _ := unstructured.NestedBool(o.Object, "spec", "parameters", "checkTolerations"); !v {
				t.Error("constraint must check tolerations")
			}
			if _, found, _ := unstructured.NestedBool(o.Object, "spec", "parameters", "checkNodeSelector"); found {
				t.Error("constraint must not check the forced node selector")
			}
		}
	}
	if assigns != 1 || constraints != 1 {
		t.Fatalf("want 1 assign + 1 constraint, got %d + %d", assigns, constraints)
	}
}
