package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type StreamMetrics struct {
	FramesPerSecond prometheus.Gauge
	EncodeLatency   prometheus.Histogram
	EncodeErrors    prometheus.Counter
	StreamUptime    prometheus.Counter
}

func NewStreamMetrics() *StreamMetrics {
	return &StreamMetrics{
		FramesPerSecond: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "stream_frames_per_second",
			Help: "Current frames per second being processed",
		}),
		EncodeLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "stream_encode_latency_seconds",
			Help:    "Histogram of encoding latency in seconds",
			Buckets: prometheus.LinearBuckets(0, 0.005, 20),
		}),
		EncodeErrors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "stream_encode_errors_total",
			Help: "T1otal number of encoding errors",
		}),
		StreamUptime: promauto.NewCounter(prometheus.CounterOpts{
			Name: "stream_uptime_seconds",
			Help: "Total streaming uptime in seconds",
		}),
	}
}
