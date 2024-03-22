package exporter

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"github.com/prometheus/client_golang/prometheus"
)

type metricName string

const (
	totalRequests         metricName = "total_requests"
	totalErrors           metricName = "total_errors"
	totalProcessingTime   metricName = "total_processing_time"
	averageProcessingTime metricName = "average_processing_time"
)

type Exporter struct {
	nc       *nats.Conn
	mutex    sync.RWMutex
	metrics  []metricInfo
	services map[string]micro.Info
}

type metricInfo struct {
	desc      *prometheus.Desc
	promType  prometheus.ValueType
	valueType metricName
	value     float64
}

// New returns a new Exporter
func New(conn *nats.Conn) *Exporter {
	return &Exporter{
		nc:       conn,
		services: make(map[string]micro.Info),
		metrics: []metricInfo{
			newCounterMetric(totalRequests, "total_num_requests"),
			newCounterMetric(totalErrors, "total_num_errors"),
			newCounterMetric(totalProcessingTime, "in seconds"),
			newCounterMetric(averageProcessingTime, "in_milliseconds"),
		},
	}
}

// WatchForServices requests service after the time interval and adds services to the map
func (e *Exporter) WatchForServices(interval int) {
	for {
		var mu sync.Mutex
		sub, err := e.nc.Subscribe(e.nc.NewRespInbox(), func(m *nats.Msg) {
			mu.Lock()
			defer mu.Unlock()
			var info micro.Info

			if err := json.Unmarshal(m.Data, &info); err != nil {
				log.Println(err)
				return
			}

			e.services[info.ID] = info
		})
		if err != nil {
			log.Println(err)
			continue
		}
		defer sub.Unsubscribe()

		subject := fmt.Sprintf("%s.%s", micro.APIPrefix, micro.InfoVerb)
		msg := nats.NewMsg(subject)
		msg.Reply = sub.Subject
		if err := e.nc.PublishMsg(msg); err != nil {
			log.Println(err)
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
}

// newCounterMetric creates a new metricInfo
func newCounterMetric(name metricName, help string) metricInfo {
	return metricInfo{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName("micro_stats", "stats", string(name)),
			help,
			[]string{"stats"},
			nil,
		),
		promType:  prometheus.CounterValue,
		valueType: name,
	}
}

// Describe satisfies the Collector interface
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, v := range e.metrics {
		ch <- v.desc
	}
}

// Collect satisfies the Collector interface
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	e.scrape(ch)
}

// scrape gathers all metrics for each service found
func (e *Exporter) scrape(ch chan<- prometheus.Metric) {
	for _, v := range e.services {
		var stats micro.Stats
		subject := fmt.Sprintf("%s.%s.%s.%s", micro.APIPrefix, micro.StatsVerb, v.Name, v.ID)
		resp, err := e.nc.Request(subject, nil, 1*time.Second)
		if err != nil && err != nats.ErrNoResponders {
			log.Println(err)
			continue
		}

		if err == nats.ErrNoResponders {
			delete(e.services, v.ID)
			continue
		}

		if err := json.Unmarshal(resp.Data, &stats); err != nil {
			log.Println(err)
			continue
		}

		for _, endpoint := range stats.Endpoints {
			labels := fmt.Sprintf("%s_%s_%s", stats.Name, stats.ID, endpoint.Name)
			for _, metric := range e.metrics {
				switch metric.valueType {
				case totalRequests:
					ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.NumRequests), labels)
				case totalErrors:
					ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.NumErrors), labels)
				case totalProcessingTime:
					ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.ProcessingTime.Seconds()), labels)
				case averageProcessingTime:
					ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.AverageProcessingTime.Milliseconds()), labels)

				}
			}

		}

	}
}
