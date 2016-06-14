package influxdb

import (
	"fmt"
	"time"

	"github.com/funkygao/go-metrics"
	log "github.com/funkygao/log4go"
	"github.com/influxdata/influxdb/client"
)

func (r *reporter) dump() {
	var pts []client.Point

	var (
		appid, topic, ver string
		tags              map[string]string
	)
	r.reg.Each(func(name string, i interface{}) {
		now := time.Now()
		appid, topic, ver, name = extractFromMetricsName(name)

		if appid == "" {
			tags = map[string]string{
				"host": r.cf.hostname,
			}
		} else {
			tags = map[string]string{
				"host":  r.cf.hostname,
				"appid": appid,
				"topic": topic,
				"ver":   ver,
			}
		}

		switch m := i.(type) {
		case metrics.Counter:
			pts = append(pts, client.Point{
				Measurement: fmt.Sprintf("%s.count", name),
				Fields: map[string]interface{}{
					"value": m.Count(),
				},
				Tags: tags,
				Time: now,
			})

		case metrics.Gauge:
			pts = append(pts, client.Point{
				Measurement: fmt.Sprintf("%s.gauge", name),
				Fields: map[string]interface{}{
					"value": m.Value(),
				},
				Tags: tags,
				Time: now,
			})

		case metrics.GaugeFloat64:
			pts = append(pts, client.Point{
				Measurement: fmt.Sprintf("%s.gauge", name),
				Fields: map[string]interface{}{
					"value": m.Value(),
				},
				Tags: tags,
				Time: now,
			})

		case metrics.Histogram:
			ps := m.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999, 0.9999})
			pts = append(pts, client.Point{
				Measurement: fmt.Sprintf("%s.histogram", name),
				Fields: map[string]interface{}{
					"count":    m.Count(),
					"max":      m.Max(),
					"mean":     m.Mean(),
					"min":      m.Min(),
					"stddev":   m.StdDev(),
					"variance": m.Variance(),
					"p50":      ps[0],
					"p75":      ps[1],
					"p95":      ps[2],
					"p99":      ps[3],
					"p999":     ps[4],
					"p9999":    ps[5],
				},
				Tags: tags,
				Time: now,
			})

		case metrics.Meter:
			pts = append(pts, client.Point{
				Measurement: fmt.Sprintf("%s.meter", name),
				Fields: map[string]interface{}{
					"count": m.Count(),
					"m1":    m.Rate1(),
					"m5":    m.Rate5(),
					"m15":   m.Rate15(),
					"mean":  m.RateMean(),
				},
				Tags: tags,
				Time: now,
			})

		case metrics.Timer:
			ps := m.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999, 0.9999})
			pts = append(pts, client.Point{
				Measurement: fmt.Sprintf("%s.timer", name),
				Fields: map[string]interface{}{
					"count":    m.Count(),
					"max":      m.Max(),
					"mean":     m.Mean(),
					"min":      m.Min(),
					"stddev":   m.StdDev(),
					"variance": m.Variance(),
					"p50":      ps[0],
					"p75":      ps[1],
					"p95":      ps[2],
					"p99":      ps[3],
					"p999":     ps[4],
					"p9999":    ps[5],
					"m1":       m.Rate1(),
					"m5":       m.Rate5(),
					"m15":      m.Rate15(),
					"meanrate": m.RateMean(),
				},
				Tags: tags,
				Time: now,
			})
		}
	})

	if r.client == nil {
		log.Warn("dump while connectin lost")
		return
	}

	_, err := r.client.Write(client.BatchPoints{
		Points:   pts,
		Database: r.cf.database,
	})
	if err != nil {
		log.Error("dump: %v", err)
	}
}
