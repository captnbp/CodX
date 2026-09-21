// Package metrics defines the CodX Prometheus metrics registry and the
// dedicated /metrics endpoint.
//
// The registry exposes the standard Go runtime metrics plus CodX-specific
// gauges: the number of running and pending workspaces and an overall
// healthiness indicator.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Gauge names registered under the "codx" namespace.
const (
	// WorkspacesRunningName counts workspace pods currently in the Running
	// phase.
	WorkspacesRunningName = "codx_workspaces_running"

	// WorkspacesPendingName counts workspace pods created but not yet
	// running (Pending phase).
	WorkspacesPendingName = "codx_workspaces_pending"

	// HealthName reports CodX overall healthiness: 1 when CodX can reach
	// its dependencies, 0 otherwise.
	HealthName = "codx_health"
)

// Metrics holds the CodX Prometheus registry and its custom gauges.
type Metrics struct {
	registry *prometheus.Registry

	workspacesRunning prometheus.Gauge
	workspacesPending prometheus.Gauge
	health            prometheus.Gauge
}

// New creates a Metrics registry with the standard Go runtime metrics and
// the CodX gauges registered.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),

		workspacesRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: WorkspacesRunningName,
			Help: "Number of workspace pods currently running.",
		}),
		workspacesPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: WorkspacesPendingName,
			Help: "Number of workspace pods pending (created but not yet running).",
		}),
		health: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: HealthName,
			Help: "CodX healthiness: 1 when healthy, 0 otherwise.",
		}),
	}

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		m.workspacesRunning,
		m.workspacesPending,
		m.health,
	)

	return m
}

// Handler returns the http.Handler serving the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// SetWorkspaceCounts updates the running and pending workspace gauges.
func (m *Metrics) SetWorkspaceCounts(running, pending int) {
	m.workspacesRunning.Set(float64(running))
	m.workspacesPending.Set(float64(pending))
}

// SetHealthy updates the healthiness gauge: 1 when healthy, 0 otherwise.
func (m *Metrics) SetHealthy(healthy bool) {
	if healthy {
		m.health.Set(1)
		return
	}
	m.health.Set(0)
}
