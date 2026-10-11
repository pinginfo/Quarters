// Package metrics holds the Prometheus metrics of the operator. They are
// served next to the controller-runtime metrics.
package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

// Phases a Tenant reports; one series per phase is exported (1 for the current one).
var Phases = []string{"Ready", "Degraded", "Terminating"}

var conditionStatuses = []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown}

var (
	// ModuleDuration observes how long a feature module takes per tenant.
	ModuleDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "quarters_module_reconcile_duration_seconds",
		Help:    "Duration of one feature module reconcile for one tenant.",
		Buckets: prometheus.DefBuckets,
	}, []string{"module"})

	// ModuleErrors counts failed module reconciles.
	ModuleErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "quarters_module_errors_total",
		Help: "Number of failed feature module reconciles.",
	}, []string{"module"})

	infoDesc = prometheus.NewDesc("quarters_tenant_info",
		"Always 1. Carries the team, profile and deletion policy of a tenant as labels.",
		[]string{"tenant", "team", "profile", "deletion_policy"}, nil)
	phaseDesc = prometheus.NewDesc("quarters_tenant_phase",
		"1 for the current phase of a tenant, 0 for the others.",
		[]string{"tenant", "phase"}, nil)
	conditionDesc = prometheus.NewDesc("quarters_tenant_condition",
		"1 for the current status (True, False, Unknown) of each tenant condition, 0 for the others.",
		[]string{"tenant", "type", "status"}, nil)
	transitionDesc = prometheus.NewDesc("quarters_tenant_condition_last_transition_timestamp_seconds",
		"Unix time at which a tenant condition last changed status.",
		[]string{"tenant", "type"}, nil)
	generationLagDesc = prometheus.NewDesc("quarters_tenant_generation_lag",
		"metadata.generation minus status.observedGeneration. Above 0 for long means the tenant is not being reconciled.",
		[]string{"tenant"}, nil)
)

// TenantCollector exports the state of every Tenant. It reads the informer
// cache when Prometheus scrapes, so there is nothing to clean up when a Tenant
// is deleted. Only the elected leader answers, so a standby replica never
// exports stale data and Prometheus sees each tenant once.
type TenantCollector struct {
	Reader  client.Reader
	Elected func() bool
}

// Register adds the collector to the controller-runtime metrics registry.
func (c *TenantCollector) Register() { ctrlmetrics.Registry.MustRegister(c) }

// Describe implements prometheus.Collector.
func (c *TenantCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{infoDesc, phaseDesc, conditionDesc, transitionDesc, generationLagDesc} {
		ch <- d
	}
}

// Collect implements prometheus.Collector.
func (c *TenantCollector) Collect(ch chan<- prometheus.Metric) {
	if c.Elected != nil && !c.Elected() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := &quartersv1alpha1.TenantList{}
	if err := c.Reader.List(ctx, list); err != nil {
		ch <- prometheus.NewInvalidMetric(infoDesc, err)
		return
	}
	for i := range list.Items {
		collectTenant(ch, &list.Items[i])
	}
}

func collectTenant(ch chan<- prometheus.Metric, t *quartersv1alpha1.Tenant) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	profile := t.Spec.ProfileRef
	if profile == "" {
		profile = "default"
	}
	policy := string(t.Spec.DeletionPolicy)
	if policy == "" {
		policy = string(quartersv1alpha1.DeletionRetain)
	}
	gauge(infoDesc, 1, t.Name, t.Spec.Team, profile, policy)

	phase := t.Status.Phase
	if !t.DeletionTimestamp.IsZero() {
		phase = "Terminating"
	}
	for _, p := range Phases {
		gauge(phaseDesc, boolValue(phase == p), t.Name, p)
	}
	for _, cond := range t.Status.Conditions {
		for _, s := range conditionStatuses {
			gauge(conditionDesc, boolValue(cond.Status == s), t.Name, cond.Type, string(s))
		}
		gauge(transitionDesc, float64(cond.LastTransitionTime.Unix()), t.Name, cond.Type)
	}
	gauge(generationLagDesc, float64(t.Generation-t.Status.ObservedGeneration), t.Name)
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func init() {
	ctrlmetrics.Registry.MustRegister(ModuleDuration, ModuleErrors)
}
