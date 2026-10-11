package controller

import (
	"reflect"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

func runtimeEqual(a, b *quartersv1alpha1.TenantConfig) bool {
	return equality.Semantic.DeepEqual(a, b)
}

// conditionsEqual ignores LastTransitionTime, which only changes with Status.
func conditionsEqual(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		x.LastTransitionTime, y.LastTransitionTime = metav1.Time{}, metav1.Time{}
		if !reflect.DeepEqual(x, y) {
			return false
		}
	}
	return true
}
