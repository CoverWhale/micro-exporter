package exporter

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsserver "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func expectedMetrics(id string) string {
	return fmt.Sprintf(`
micro_stats_stats_average_processing_time{stats="test_%s_tests"} 0
micro_stats_stats_total_errors{stats="test_%s_test"} 0
micro_stats_stats_total_processing_time{stats="test_%s_test"} 0
micro_stats_stats_total_requests{stats="test_%s_test"} 0
`, id, id, id, id)
}

func newServer(t *testing.T) *server.Server {
	t.Helper()
	opts := natsserver.DefaultTestOptions
	opts.Port = -1
	return natsserver.RunServer(&opts)
}

func shutdownJSServerAndRemoveStorage(t *testing.T, s *server.Server) {
	t.Helper()
	s.Shutdown()
	s.WaitForShutdown()
}

func startMicro(nc *nats.Conn) (micro.Service, error) {
	config := micro.Config{
		Name:        "test",
		Version:     "0.0.1",
		Description: "testing",
	}
	svc, err := micro.AddService(nc, config)
	if err != nil {
		return nil, err
	}

	svc.AddEndpoint("test", micro.HandlerFunc(func(r micro.Request) { r.Respond([]byte("ok")) }), micro.WithEndpointSubject("testing"))

	return svc, nil
}

func TestScrape(t *testing.T) {
	tt := []struct {
		name     string
		requests int
		err      error
	}{
		{name: "1 request", requests: 1, err: nil},
		{name: "2 requests", requests: 2, err: nil},
		{name: "10 requests", requests: 10, err: nil},
	}

	for _, v := range tt {
		t.Run(v.name, func(t *testing.T) {
			server := newServer(t)
			nc, err := nats.Connect(server.ClientURL())
			if err != nil {
				t.Fatal(err)
			}

			svc, err := startMicro(nc)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Stop()

			e := New(nc)
			e.scrapeServices(1)
			for range v.requests {
				if _, err := nc.Request("testing", nil, 1*time.Second); err != nil {
					t.Fatal(err)
				}
			}

			numMetrics := testutil.CollectAndCount(e)

			if len(e.services) != 1 {
				t.Errorf("expected 1 service but got %d", len(e.services))
			}

			if numMetrics != len(setupDefaultMetrics()) {
				t.Errorf("expected %d metrics but got %d metrics", len(setupDefaultMetrics()), numMetrics)
			}

			ch := make(chan prometheus.Metric, 4)
			go func(ch <-chan prometheus.Metric) {
				var m = &dto.Metric{}
				val := <-ch
				err := val.Write(m)
				if err != nil {
					t.Error(err)
				}

				// only have 1 request sent so this should always equal 1
				total := m.Counter.Value
				if *total != float64(v.requests) {
					t.Errorf("expected value of %d but got %v", v.requests, *total)
				}

			}(ch)

			e.Collect(ch)

			expectedMetrics := expectedMetrics(svc.Info().ID)
			if err := testutil.CollectAndCompare(e, strings.NewReader(expectedMetrics), string(totalRequests), string(totalErrors), string(totalProcessingTime), string(averageProcessingTime)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
