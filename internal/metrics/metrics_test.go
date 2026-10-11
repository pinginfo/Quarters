package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

func gather(t *testing.T, c *TenantCollector) map[string][]*dto.Metric {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]*dto.Metric{}
	for _, mf := range mfs {
		out[mf.GetName()] = mf.GetMetric()
	}
	return out
}

func label(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func find(ms []*dto.Metric, want map[string]string) *dto.Metric {
	for _, m := range ms {
		ok := true
		for k, v := range want {
			ok = ok && label(m, k) == v
		}
		if ok {
			return m
		}
	}
	return nil
}

func TestTenantCollector(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = quartersv1alpha1.AddToScheme(scheme)
	tenant := &quartersv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Generation: 3},
		Spec:       quartersv1alpha1.TenantSpec{Team: "backend"},
		Status: quartersv1alpha1.TenantStatus{
			Phase: "Degraded", ObservedGeneration: 2,
			Conditions: []metav1.Condition{{Type: "QuotaReady", Status: metav1.ConditionFalse, LastTransitionTime: metav1.Unix(1000, 0)}},
		},
	}
	c := &TenantCollector{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tenant).Build(), Elected: func() bool { return true }}

	got := gather(t, c)

	if m := find(got["quarters_tenant_info"], map[string]string{"tenant": "app", "team": "backend", "profile": "default", "deletion_policy": "Retain"}); m == nil || m.GetGauge().GetValue() != 1 {
		t.Error("info series missing: profile and deletion policy must default")
	}
	if find(got["quarters_tenant_phase"], map[string]string{"phase": "Degraded"}).GetGauge().GetValue() != 1 ||
		find(got["quarters_tenant_phase"], map[string]string{"phase": "Ready"}).GetGauge().GetValue() != 0 {
		t.Error("only the current phase must be 1")
	}
	if find(got["quarters_tenant_condition"], map[string]string{"type": "QuotaReady", "status": "False"}).GetGauge().GetValue() != 1 {
		t.Error("condition status False must be 1")
	}
	if find(got["quarters_tenant_condition_last_transition_timestamp_seconds"], map[string]string{"type": "QuotaReady"}).GetGauge().GetValue() != 1000 {
		t.Error("transition time not exported")
	}
	if find(got["quarters_tenant_generation_lag"], map[string]string{"tenant": "app"}).GetGauge().GetValue() != 1 {
		t.Error("generation lag should be 3-2")
	}
}

func TestTenantCollectorStaysSilentWhenNotLeader(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = quartersv1alpha1.AddToScheme(scheme)
	tenant := &quartersv1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: "app"}}
	c := &TenantCollector{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tenant).Build(), Elected: func() bool { return false }}
	if got := gather(t, c); len(got) != 0 {
		t.Errorf("a standby replica exported %d metric families", len(got))
	}
}
