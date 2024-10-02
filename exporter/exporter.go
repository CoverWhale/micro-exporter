// Copyright 2024 Cover Whale Insurance Solutions Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License

package exporter

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/CoverWhale/logr"
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
		metrics:  setupDefaultMetrics(),
	}
}

func setupDefaultMetrics() []metricInfo {
	return []metricInfo{
		newCounterMetric(totalRequests, "total_num_requests"),
		newCounterMetric(totalErrors, "total_num_errors"),
		newCounterMetric(totalProcessingTime, "in seconds"),
		newCounterMetric(averageProcessingTime, "in_milliseconds"),
	}

}

// WatchForServices requests service after the time interval and adds services to the map
func (e *Exporter) WatchForServices(interval int) {
	for {
		e.scrapeServices(interval)
	}
}

func (e *Exporter) scrapeServices(interval int) {
	var mu sync.Mutex
	sub, err := e.nc.Subscribe(e.nc.NewRespInbox(), func(m *nats.Msg) {
		mu.Lock()
		defer mu.Unlock()
		var info micro.Info

		if !json.Valid(m.Data) {
			return
		}

		if err := json.Unmarshal(m.Data, &info); err != nil {
			logr.Error(err)
			return
		}

		e.services[info.ID] = info
	})
	if err != nil {
		logr.Error(err)
		return
	}
	defer sub.Unsubscribe()

	subject := fmt.Sprintf("%s.%s", micro.APIPrefix, micro.InfoVerb)
	msg := nats.NewMsg(subject)
	msg.Reply = sub.Subject
	if err := e.nc.PublishMsg(msg); err != nil {
		logr.Error(err)
	}
	time.Sleep(time.Duration(interval) * time.Second)
}

// newCounterMetric creates a new metricInfo
func newCounterMetric(name metricName, help string) metricInfo {
	return metricInfo{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName("micro_stats", "stats", string(name)),
			help,
			[]string{"service", "instance_id", "name", "subject"},
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
	var wg sync.WaitGroup
	wg.Add(len(e.services))
	for _, service := range e.services {
		go e.scrapeService(&wg, service, ch)
	}
	wg.Wait()
}

func (e *Exporter) scrapeService(wg *sync.WaitGroup, service micro.Info, ch chan<- prometheus.Metric) {
	defer wg.Done()
	var stats micro.Stats
	subject := fmt.Sprintf("%s.%s.%s.%s", micro.APIPrefix, micro.StatsVerb, service.Name, service.ID)
	resp, err := e.nc.Request(subject, nil, 1*time.Second)
	if err != nil && err != nats.ErrNoResponders {
		logr.Error(err)
		return
	}

	if err == nats.ErrNoResponders {
		delete(e.services, service.ID)
		return
	}

	if err := json.Unmarshal(resp.Data, &stats); err != nil {
		logr.Error(err)
		return
	}
	wg.Add(len(stats.Endpoints))
	for _, endpoint := range stats.Endpoints {
		go e.scrapeStats(wg, stats, endpoint, ch)
	}
}

func (e *Exporter) scrapeStats(wg *sync.WaitGroup, stats micro.Stats, endpoint *micro.EndpointStats, ch chan<- prometheus.Metric) {
	defer wg.Done()
	labels := []string{stats.Name, stats.ID, endpoint.Name, endpoint.Subject}
	for _, metric := range e.metrics {
		switch metric.valueType {
		case totalRequests:
			ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.NumRequests), labels...)
		case totalErrors:
			ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.NumErrors), labels...)
		case totalProcessingTime:
			ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.ProcessingTime.Seconds()), labels...)
		case averageProcessingTime:
			ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.CounterValue, float64(endpoint.AverageProcessingTime.Milliseconds()), labels...)

		}
	}

}
