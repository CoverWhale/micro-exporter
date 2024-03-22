package cmd

import (
	"fmt"
	"net/http"
	"os"

	"github.com/CoverWhale/micro-exporter/exporter"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "microexporter",
	Short: "Prometheus exporter for NATS microservices",
	RunE:  start,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	//rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.micro-exporter.yaml)")

	rootCmd.Flags().Int("port", 8080, "exporter port")
	viper.BindPFlag("port", rootCmd.Flags().Lookup("port"))
	rootCmd.Flags().StringP("server", "s", "nats://localhost:4222", "NATS URLs")
	viper.BindPFlag("server", rootCmd.Flags().Lookup("server"))
	rootCmd.Flags().String("creds", "", "User credentials")
	viper.BindPFlag("creds", rootCmd.Flags().Lookup("creds"))
	rootCmd.Flags().String("jwt", "", "User JWT")
	viper.BindPFlag("jwt", rootCmd.Flags().Lookup("jwt"))
	rootCmd.Flags().String("seed", "", "User seed")
	viper.BindPFlag("seed", rootCmd.Flags().Lookup("seed"))
	rootCmd.Flags().Int("scrape-interval", 15, "Scrape interval to look up new services in seconds")
	viper.BindPFlag("scrape_interval", rootCmd.Flags().Lookup("scrape-interval"))
}

func start(cmd *cobra.Command, args []string) error {

	nc, err := nats.Connect(viper.GetString("server"), nats.UserCredentials(viper.GetString("creds")))
	if err != nil {
		return err
	}

	ex := exporter.New(nc)
	prometheus.MustRegister(ex)
	go ex.WatchForServices(viper.GetInt("scrape_interval"))

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resp := fmt.Sprintf("<html>" +
			"<head><title>Micro Stats Exporter</title></head>" +
			"<body>\n<h1>Micro Stats Exporter</h1>" +
			"<p><a href='/metrics'>Metrics</a></p>" +
			"</body>\n</html>")
		fmt.Fprint(w, resp)
	})

	port := fmt.Sprintf(":%d", viper.GetInt("port"))
	fmt.Println("starting webserver")
	return http.ListenAndServe(port, nil)

}
