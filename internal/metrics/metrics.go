// Package metrics holds the Prometheus metrics of the operator. They are
// served next to the controller-runtime metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

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
)

func init() {
	ctrlmetrics.Registry.MustRegister(ModuleDuration, ModuleErrors)
}
