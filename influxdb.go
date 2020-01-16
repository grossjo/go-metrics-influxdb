package influxdb

import (
	"fmt"
	"log"
	uurl "net/url"
	"time"

	client "github.com/influxdata/influxdb1-client"
	"github.com/rcrowley/go-metrics"
)

type reporter struct {
	reg      metrics.Registry
	interval time.Duration
	align    bool
	url      uurl.URL
	database string

	measurement string
	username    string
	password    string
	tags        map[string]string

	client *client.Client
}

// InfluxDB starts a InfluxDB reporter which will post the metrics from the given registry at each d interval.
func InfluxDB(r metrics.Registry, d time.Duration, url, database, measurement, username, password string, align bool) {
	InfluxDBWithTags(r, d, url, database, measurement, username, password, map[string]string{}, align)
}

func InfluxDBHandler(r metrics.Registry, writer http.ResponseWriter, request *http.Request, d time.Duration, url, database, measurement, username, password string, align bool) {
	InfluxDBWithTagsHandler(r, writer, request, d, url, database, measurement, username, password, map[string]string{}, align)
}

// InfluxDBWithTags starts a InfluxDB reporter which will post the metrics from the given registry at each d interval with the specified tags
func InfluxDBWithTags(r metrics.Registry, d time.Duration, url, database, measurement, username, password string, tags map[string]string, align bool) {
	u, err := uurl.Parse(url)
	if err != nil {
		log.Printf("unable to parse InfluxDB url %s. err=%v", url, err)
		return
	}

	rep := &reporter{
		reg:         r,
		interval:    d,
		url:         *u,
		database:    database,
		measurement: measurement,
		username:    username,
		password:    password,
		tags:        tags,
		align:       align,
	}
	if err := rep.makeClient(); err != nil {
		log.Printf("unable to make InfluxDB client. err=%v", err)
		return
	}

	rep.run()
}

// InfluxDBWithTags starts a InfluxDB reporter which will post the metrics from the given registry at each d interval with the specified tags
func InfluxDBWithTagsHandler(r metrics.Registry, writer http.ResponseWriter, request *http.Request, d time.Duration, url, database, measurement, username, password string, tags map[string]string, align bool) {
	u, err := uurl.Parse(url)
	if err != nil {
		log.Printf("unable to parse InfluxDB url %s. err=%v", url, err)
		return
	}

	rep := &reporter{
		reg:         r,
		interval:    d,
		url:         *u,
		database:    database,
		measurement: measurement,
		username:    username,
		password:    password,
		tags:        tags,
		align:       align,
	}
	// remains to be tested
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprintf(w, "{\n")
	first := true
	expvar.Do(func(kv expvar.KeyValue) {
		if !first {
			fmt.Fprintf(w, ",\n")
		}
		first = false
		fmt.Fprintf(w, "%q: %s", kv.Key, kv.Value)
	})
	fmt.Fprintf(w, "\n}\n")

	rep.run()
}

func (r *reporter) makeClient() (err error) {
	r.client, err = client.NewClient(client.Config{
		URL:      r.url,
		Username: r.username,
		Password: r.password,
	})

	return
}

func (r *reporter) run() {
	intervalTicker := time.Tick(r.interval)
	pingTicker := time.Tick(time.Second * 5)

	for {
		select {
		case <-intervalTicker:
			if err := r.send(); err != nil {
				log.Printf("unable to send metrics to InfluxDB. err=%v", err)
			}
		case <-pingTicker:
			_, _, err := r.client.Ping()
			if err != nil {
				log.Printf("got error while sending a ping to InfluxDB, trying to recreate client. err=%v", err)

				if err = r.makeClient(); err != nil {
					log.Printf("unable to make InfluxDB client. err=%v", err)
				}
			}
		}
	}
}

func (r *reporter) send() error {
	var pts []client.Point

	now := time.Now()
	if r.align {
		now = now.Truncate(r.interval)
	}
	r.reg.Each(func(name string, i interface{}) {

		switch metric := i.(type) {
		case metrics.Counter:
			ms := metric.Snapshot()
			pts = append(pts, client.Point{
				Measurement: r.measurement,
				Tags:        r.tags,
				Fields: map[string]interface{}{
					fmt.Sprintf("%s.count", name): ms.Count(),
				},
				Time: now,
			})
		case metrics.Gauge:
			ms := metric.Snapshot()
			pts = append(pts, client.Point{
				Measurement: r.measurement,
				Tags:        r.tags,
				Fields: map[string]interface{}{
					fmt.Sprintf("%s.gauge", name): ms.Value(),
				},
				Time: now,
			})
		case metrics.GaugeFloat64:
			ms := metric.Snapshot()
			pts = append(pts, client.Point{
				Measurement: r.measurement,
				Tags:        r.tags,
				Fields: map[string]interface{}{
					fmt.Sprintf("%s.gauge", name): ms.Value(),
				},
				Time: now,
			})
		case metrics.Histogram:
			ms := metric.Snapshot()
			ps := ms.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999, 0.9999})
			fields := map[string]interface{}{
				"count":    ms.Count(),
				"max":      ms.Max(),
				"mean":     ms.Mean(),
				"min":      ms.Min(),
				"stddev":   ms.StdDev(),
				"variance": ms.Variance(),
				"p50":      ps[0],
				"p75":      ps[1],
				"p95":      ps[2],
				"p99":      ps[3],
				"p999":     ps[4],
				"p9999":    ps[5],
			}
			for k, v := range fields {
				these_tags := map[string]string{}
				for tk, tv := range r.tags {
					these_tags[tk] = tv
				}
				these_tags["bucket"] = k
				pts = append(pts, client.Point{
					Measurement: r.measurement,
					Tags:        these_tags,
					Fields: map[string]interface{}{
						fmt.Sprintf("%s.histogram", name): v,
					},
					Time: now,
				})

			}
		case metrics.Meter:
			ms := metric.Snapshot()
			fields := map[string]interface{}{
				"count": ms.Count(),
				"m1":    ms.Rate1(),
				"m5":    ms.Rate5(),
				"m15":   ms.Rate15(),
				"mean":  ms.RateMean(),
			}
			for k, v := range fields {
				these_tags := map[string]string{}
				for tk, tv := range r.tags {
					these_tags[tk] = tv
				}
				these_tags["bucket"] = k
				pts = append(pts, client.Point{
					Measurement: r.measurement,
					Tags:        these_tags,
					Fields: map[string]interface{}{
						fmt.Sprintf("%s.meter", name): v,
					},
					Time: now,
				})
			}

		case metrics.Timer:
			ms := metric.Snapshot()
			ps := ms.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999, 0.9999})
			fields := map[string]interface{}{
				"count":    ms.Count(),
				"max":      ms.Max(),
				"mean":     ms.Mean(),
				"min":      ms.Min(),
				"stddev":   ms.StdDev(),
				"variance": ms.Variance(),
				"p50":      ps[0],
				"p75":      ps[1],
				"p95":      ps[2],
				"p99":      ps[3],
				"p999":     ps[4],
				"p9999":    ps[5],
				"m1":       ms.Rate1(),
				"m5":       ms.Rate5(),
				"m15":      ms.Rate15(),
				"meanrate": ms.RateMean(),
			}
			for k, v := range fields {
				these_tags := map[string]string{}
				for tk, tv := range r.tags {
					these_tags[tk] = tv
				}
				these_tags["bucket"] = k
				data, ok := v.(float64)
				if !ok {

					data = float64(v.(int64))
				}
				pts = append(pts, client.Point{
					Measurement: r.measurement,
					Tags:        these_tags,
					Fields: map[string]interface{}{
						fmt.Sprintf("%s.timer", name): data,
					},
					Time: now,
				})
			}
		}
	})

	bps := client.BatchPoints{
		Points:   pts,
		Database: r.database,
	}

	_, err := r.client.Write(bps)
	return err
}
