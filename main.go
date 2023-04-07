package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	promVersion "github.com/prometheus/common/version"
)

type apiResponse struct {
	Result struct {
		WattHoursDay map[string]int `json:"watt_hours_day"`
	} `json:"result"`
}

func init() {
	promVersion.Version = "0.1.0"
	prometheus.MustRegister(promVersion.NewCollector("forecast_solar_exporter"))
}

type forecastCollector struct {
	metric *prometheus.Desc
	Date   time.Time
	Kwh    float64
}

func (c *forecastCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.metric
}

func (c *forecastCollector) Collect(ch chan<- prometheus.Metric) {
	s := prometheus.NewMetricWithTimestamp(c.Date, prometheus.MustNewConstMetric(c.metric, prometheus.GaugeValue, c.Kwh))
	ch <- s
}

func main() {
	var (
		listenAddr   = flag.String("listen-address", ":9111", "The address to listen on for HTTP requests.")
		latitude     = flag.String("latitude", "54.9", "Latitude of your location")
		longitude    = flag.String("longitude", "25.3", "Longitude of your location")
		declination  = flag.String("declination", "45", "Solar plane declination, 0 = horizontal, 90 = vertical")
		az           = flag.String("az", "0", "Solar plane azimuth, West = 90, South = 0, East = -90")
		kwp          = flag.String("kWp", "10", "Solar plane max. peak power in kilo watt")
		pollInterval = flag.Int("poll-interval", 3600, "Interval in seconds between polls.")
		showVersion  = flag.Bool("version", false, "Print version information and exit.")
	)

	flag.Parse()

	if *showVersion {
		fmt.Printf("%s\n", promVersion.Print("forecast_solar_exporter"))
		os.Exit(0)
	}

	today := &forecastCollector{
		metric: prometheus.NewDesc(
			"forecast_solar_today",
			"Solar harvest forecast for today",
			nil,
			nil,
		),
	}
	tomorrow := &forecastCollector{
		metric: prometheus.NewDesc(
			"forecast_solar_tomorrow",
			"Solar harvest forecast for tomorrow",
			nil,
			nil,
		),
	}

	// Register the summary and the histogram with Prometheus's default registry
	prometheus.MustRegister(today)
	prometheus.MustRegister(tomorrow)

	// Add Go module build info
	prometheus.MustRegister(collectors.NewBuildInfoCollector())

	// Poll loop
	go func() {
		for {
			// Use anonymous function so we can defer nicely
			func() {
				defer time.Sleep(time.Duration(*pollInterval) * time.Second)
				res := &apiResponse{}

				var client = &http.Client{Timeout: 10 * time.Second}
				url := fmt.Sprintf("https://api.forecast.solar/estimate/%s/%s/%s/%s/%s", *latitude, *longitude, *declination, *az, *kwp)

				r, err := client.Get(url)
				if err != nil {
					log.Printf("Error getting URL: %s", err)
					return
				}
				defer r.Body.Close()

				if r.StatusCode != 200 {
					log.Printf("Error while requesting URL: %s", r.Status)
					return
				}

				if err := json.NewDecoder(r.Body).Decode(res); err != nil {
					log.Printf("Error decoding JSON: %s", err)
					return
				}

				// Hack to make sure first entry is today, second is tomorrow
				sortedForecast := make([]string, 0, len(res.Result.WattHoursDay))
				for date := range res.Result.WattHoursDay {
					sortedForecast = append(sortedForecast, date)
				}
				sort.Strings(sortedForecast)

				for i, date := range sortedForecast {
					t, err := time.Parse(time.DateOnly, date)
					if err != nil {
						log.Printf("Error parsing date: %s", err)
						return
					}
					kwh := res.Result.WattHoursDay[date]

					if i == 0 {
						today.Date = t
						today.Kwh = float64(kwh)
					} else if i == 1 {
						tomorrow.Date = t
						tomorrow.Kwh = float64(kwh)

					} else {
						log.Println("Error: Unexpected entry")
						return
					}
				}

			}()
		}
	}()

	// Expose the registered metrics via HTTP
	http.Handle("/metrics", promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{},
	))
	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}
