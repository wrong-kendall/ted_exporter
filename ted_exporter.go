package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/log"
)

const subsystem = "exporter"

var (
	Version = "0.0.0.dev"

	listenAddress = flag.String("web.listen-address", ":9191", "Address on which to expose metrics and web interface.")
	metricsPath   = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	postRate   = flag.Int("post-rate", 1, "How frequently clients should post (in minutes)")
)

var (
	watts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watts",
			Help: "The watts used by a device.",
		},
		[]string{"mtu"},
	)
	updatesPerPost = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "updates_per_post",
			Help: "The number of updates per post.",
		},
		[]string{"mtu"},
	)
	voltage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "voltage",
			Help: "The last voltage reported by the device.",
		},
		[]string{"mtu"},
	)
)

type ted5000ActivationRequest struct {
	XMLName xml.Name `xml:"ted5000Activation"`
	Gateway string   `xml:"Gateway"`
	Unique  string   `xml:"Unique"`
	Version string   `xml:"Ver"`
}

type ted5000ActivationResponse struct {
	PostServer string
	UseSSL     bool
	PostPort   int
	PostURL    string
	AuthToken  string
	PostRate   int
	HighPrec   string //TODO(kendall): This should be 1 byte (probably enum)
}

type ted5000 struct {
	GWID string `xml:"GWID,attr"`
	Auth string `xml:auth,attr"`
	COST COST
	MTU []MTU
}

type COST struct { //TODO(kendall): Lowercase?
	Mrd int `xml:"mrd,attr"`
	Fixed float64 `xml:"fixed,attr"`
	Min float64 `xml:"min,attr"`
}

//TODO(kendall): Handle demand

type MTU struct {
	ID string `xml:"ID,attr"`
	Type string `xml:"type,attr"`
	Version string `xml:"ver,attr"`
	Cumulative []Cumulative `xml:"cumulative"`
}

type Cumulative struct {
	Timestamp int64 `xml:"timestamp,attr"`
	Watts float64 `xml:"watts,attr"`
	Rate float64 `xml:"rate,attr"` //TODO(kendall): Rename this Price?
	Pf float64 `xml:"pf,attr"`
	Voltage float64 `xml:"voltage,attr"`
}
type cumulativeSorter struct {
	cumulatives []Cumulative
	by      func(p1, p2 *Cumulative) bool
}
type By func(c1, c2 *Cumulative) bool
func (by By) Sort(cumulatives []Cumulative) {
	cs := &cumulativeSorter{
		cumulatives: cumulatives,
		by:      by,
	}
	sort.Sort(cs)
}
func (s *cumulativeSorter) Len() int {
	return len(s.cumulatives)
}
func (s *cumulativeSorter) Swap(i, j int) {
	s.cumulatives[i], s.cumulatives[j] = s.cumulatives[j], s.cumulatives[i]
}
func (s *cumulativeSorter) Less(i, j int) bool {
	return s.by(&s.cumulatives[i], &s.cumulatives[j])
}

func postHandler(w http.ResponseWriter, r *http.Request) {
	// TODO(kendall): Count posts (success, failure, by GWID)
	// TODO(kendall): Fix error handling
	timestamp := func(c1, c2 *Cumulative) bool {
		return c1.Timestamp < c2.Timestamp
	}
	var ted ted5000
	if err := xml.NewDecoder(r.Body).Decode(&ted); err != nil {
		fmt.Fprintf(w, "Could not parse post XML: %s", err)
	}
	log.Debugf("Update post: %s", ted)
	for i := 0; i < len(ted.MTU); i++ {
		updates := ted.MTU[i].Cumulative
		By(timestamp).Sort(updates)
		for j :=0; j < len(updates); j++ {
			updatesPerPost.WithLabelValues(ted.MTU[i].ID).Inc()
			watts.WithLabelValues(ted.MTU[i].ID).Set(updates[j].Watts)
			voltage.WithLabelValues(ted.MTU[i].ID).Set(updates[j].Voltage)
		}
	}
	fmt.Fprintf(w, "Ok")
}

func activateHandler(w http.ResponseWriter, r *http.Request) {
	// TODO(kendall): Count activations
	// TODO(kendall): Return errors or at least clean up the logic
	// TODO(kendall): Flag for ssl?
	// TODO(kendall): Figure out host and port
	// TODO(kendall): Authtoken
	// TODO(kendall): postrate
	// TODO(kendall): highprec
	var activation ted5000ActivationRequest
	var port_suffix string
	var port int
	var err error
	if err := xml.NewDecoder(r.Body).Decode(&activation); err != nil {
		fmt.Fprintf(w, "Could not parse activation XML: %s", err)
	}
	log.Debugf("Activation request: %s", activation)
	if _, port_suffix, err = net.SplitHostPort(*listenAddress); err != nil {
		fmt.Printf("Could not determine port from %s: %s", *listenAddress, err)
	}
	if port, err = strconv.Atoi(port_suffix); err != nil {
		fmt.Printf("Could not create port (%s) to int: %s", port, err)
	}
	if err := xml.NewEncoder(w).Encode(ted5000ActivationResponse{
		PostServer: r.Host,
		UseSSL: false,
		PostPort: port,
		PostRate: *postRate,
		PostURL: "/post",
		HighPrec: "T"}); err != nil {
		fmt.Fprintf(w, "Could not create XML activation response: %s", err)
	}
}

func main() {
	flag.Parse()

	handler := prometheus.Handler()
	prometheus.MustRegister(watts)
	prometheus.MustRegister(updatesPerPost)
	prometheus.MustRegister(voltage)

	http.Handle(*metricsPath, handler)
	http.HandleFunc("/activate", activateHandler)
	http.HandleFunc("/post", postHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>TED Exporter</title></head>
			<body>
			<h1>TED Exporter</h1>
			<p><a href="` + *metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})

	log.Infof("Starting ted_exporter v%s at %s", Version, *listenAddress)
	err := http.ListenAndServe(*listenAddress, nil)
	if err != nil {
		log.Fatal(err)
	}
}
