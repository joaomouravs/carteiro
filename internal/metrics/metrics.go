// Package metrics exposes Carteiro's Prometheus metrics (FR-20).
//
// Every process serves them on their own listener (CARTEIRO_METRICS_ADDR),
// not on the public API: queue depth and delivery rates are operational
// detail, and the worker has no other HTTP server to serve them on.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/joaomouravs/carteiro/internal/store"
)

// Metrics holds the process's metrics and the registry that serves them.
type Metrics struct {
	registry   *prometheus.Registry
	deliveries *prometheus.CounterVec
	duration   prometheus.Histogram
}

// New returns the metrics every process has: the Go runtime and process
// metrics, plus delivery metrics, which stay at zero outside a worker.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "carteiro_deliveries_total",
			Help: "Delivery attempts by outcome: succeeded, retry (failed, will be retried), " +
				"dead (failed for the last time) or stale (result discarded: the lease was lost).",
		}, []string{"outcome"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "carteiro_delivery_duration_seconds",
			Help: "Duration of each delivery attempt, from the request to the end of the response.",
			// From a fast local receiver to the 15s default timeout.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 15},
		}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.deliveries,
		m.duration,
	)
	// Every outcome exists from the start, at zero, so a rate() over it
	// works before the first delivery of that kind.
	for _, o := range []string{"succeeded", "retry", "dead", "stale"} {
		m.deliveries.WithLabelValues(o)
	}
	return m
}

// ObserveAttempt records one delivery attempt. It implements
// delivery.Recorder.
func (m *Metrics) ObserveAttempt(outcome string, duration time.Duration) {
	m.deliveries.WithLabelValues(outcome).Inc()
	m.duration.Observe(duration.Seconds())
}

// RegisterQueue adds the queue metrics, read from the database at each
// scrape. Only worker processes register them: the numbers are the same
// from every process, and one source is enough.
func (m *Metrics) RegisterQueue(q *store.Queries, logger *slog.Logger) {
	m.registry.MustRegister(&queueCollector{q: q, logger: logger})
}

// Handler serves the metrics in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

var (
	queueDepth = prometheus.NewDesc("carteiro_queue_depth",
		"Deliveries waiting to be sent (pending, due or scheduled for a retry) or being sent (in_flight).",
		[]string{"status"}, nil)
	oldestDueAge = prometheus.NewDesc("carteiro_oldest_pending_age_seconds",
		"How long the most overdue pending delivery has been due without a worker taking it. "+
			"Growing means workers are falling behind, before anything fails; 0 when none is overdue.",
		nil, nil)
)

// scrapeTimeout bounds the query behind one scrape, below Prometheus's
// default 10s scrape timeout.
const scrapeTimeout = 2 * time.Second

// queueCollector reads the queue metrics when Prometheus scrapes, so they
// are never stale and cost nothing between scrapes.
type queueCollector struct {
	q      *store.Queries
	logger *slog.Logger
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- queueDepth
	ch <- oldestDueAge
}

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()
	s, err := c.q.QueueStats(ctx)
	if err != nil {
		// An invalid metric fails the scrape visibly, which is better than
		// reporting an empty queue that is not empty.
		c.logger.Error("collect queue metrics", "err", err)
		ch <- prometheus.NewInvalidMetric(queueDepth, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(queueDepth, prometheus.GaugeValue, float64(s.Pending), "pending")
	ch <- prometheus.MustNewConstMetric(queueDepth, prometheus.GaugeValue, float64(s.InFlight), "in_flight")
	ch <- prometheus.MustNewConstMetric(oldestDueAge, prometheus.GaugeValue, s.OldestDueAgeSeconds)
}
