package metrics

import "github.com/prometheus/client_golang/prometheus"

// spoolMetrics are the xi_video_spool_* metrics (WP-121b): the spool files
// of large video sources and outputs.
type spoolMetrics struct {
	bytes    prometheus.Gauge
	files    prometheus.Gauge
	spills   *prometheus.CounterVec
	rejected prometheus.Counter
}

func newSpoolMetrics() *spoolMetrics {
	return &spoolMetrics{
		bytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_spool_bytes",
			Help:      "Bytes reserved by video spool files (written or about to be), counted against XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES.",
		}),
		files: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_spool_files",
			Help:      "Open video spool files.",
		}),
		spills: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "video_spool_spills_total",
			Help:      "Video sources (input) and transcode outputs (output) kept in a spool file instead of memory.",
		}, []string{"kind"}),
		rejected: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "video_spool_rejections_total",
			Help:      "Requests refused (503) because the video spool budget was full.",
		}),
	}
}

func (s *spoolMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{s.bytes, s.files, s.spills, s.rejected}
}

// SpoolObserver is the hook set of internal/spool (spool.Metrics).
type SpoolObserver interface {
	Usage(bytes int64, files int)
	// Spilled counts a spool file of kind "input" or "output" (anything
	// else is "other").
	Spilled(kind string)
	Rejected()
}

// Spool returns the SpoolObserver backed by m.
func (m *Metrics) Spool() SpoolObserver { return spoolHooks{s: m.spool} }

type spoolHooks struct{ s *spoolMetrics }

func (h spoolHooks) Usage(bytes int64, files int) {
	h.s.bytes.Set(float64(bytes))
	h.s.files.Set(float64(files))
}

func (h spoolHooks) Spilled(kind string) {
	if kind != "input" && kind != "output" {
		kind = "other"
	}
	h.s.spills.WithLabelValues(kind).Inc()
}

func (h spoolHooks) Rejected() { h.s.rejected.Inc() }
