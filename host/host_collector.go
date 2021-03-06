package host

import (
	"fmt"
	"sync"

	"github.com/czerwonk/ovirt_api/api"
	"github.com/czerwonk/ovirt_exporter/cluster"
	"github.com/czerwonk/ovirt_exporter/metric"
	"github.com/czerwonk/ovirt_exporter/network"
	"github.com/czerwonk/ovirt_exporter/statistic"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

const prefix = "ovirt_host_"

var (
	upDesc         *prometheus.Desc
	cpuCoresDesc   *prometheus.Desc
	cpuSocketsDesc *prometheus.Desc
	cpuThreadsDesc *prometheus.Desc
	cpuSpeedDesc   *prometheus.Desc
	memoryDesc     *prometheus.Desc
	labelNames     []string
)

func init() {
	labelNames = []string{"name", "cluster"}
	upDesc = prometheus.NewDesc(prefix+"up", "Host is running (1) or not (0)", labelNames, nil)
	cpuCoresDesc = prometheus.NewDesc(prefix+"cpu_cores", "Number of CPU cores assigned", labelNames, nil)
	cpuSocketsDesc = prometheus.NewDesc(prefix+"cpu_sockets", "Number of sockets", labelNames, nil)
	cpuThreadsDesc = prometheus.NewDesc(prefix+"cpu_threads", "Number of threads", labelNames, nil)
	cpuSpeedDesc = prometheus.NewDesc(prefix+"cpu_speed_hertz", "CPU speed in hertz", labelNames, nil)
	memoryDesc = prometheus.NewDesc(prefix+"memory_installed_bytes", "Memory installed in bytes", labelNames, nil)
}

// HostCollector collects host statistics from oVirt
type HostCollector struct {
	client          *api.Client
	collectDuration prometheus.Observer
	metrics         []prometheus.Metric
	collectNetwork  bool
	mutex           sync.Mutex
}

// NewCollector creates a new collector
func NewCollector(client *api.Client, collectNetwork bool, collectDuration prometheus.Observer) prometheus.Collector {
	return &HostCollector{client: client, collectNetwork: collectNetwork, collectDuration: collectDuration}
}

// Collect implements Prometheus Collector interface
func (c *HostCollector) Collect(ch chan<- prometheus.Metric) {
	for _, m := range c.getMetrics() {
		ch <- m
	}
}

// Describe implements Prometheus Collector interface
func (c *HostCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range c.getMetrics() {
		ch <- m.Desc()
	}
}

func (c *HostCollector) getMetrics() []prometheus.Metric {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.metrics != nil {
		return c.metrics
	}

	c.retrieveMetrics()
	return c.metrics
}

func (c *HostCollector) retrieveMetrics() {
	timer := prometheus.NewTimer(c.collectDuration)
	defer timer.ObserveDuration()

	h := Hosts{}
	err := c.client.GetAndParse("hosts", &h)
	if err != nil {
		log.Error(err)
		return
	}

	wg := &sync.WaitGroup{}
	wg.Add(len(h.Hosts))

	ch := make(chan prometheus.Metric)
	for _, h := range h.Hosts {
		go c.collectForHost(h, ch, wg)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for m := range ch {
		c.metrics = append(c.metrics, m)
	}
}

func (c *HostCollector) collectForHost(host Host, ch chan prometheus.Metric, wg *sync.WaitGroup) {
	defer wg.Done()

	h := &host
	l := []string{h.Name, cluster.Name(h.Cluster.ID, c.client)}

	ch <- c.upMetric(h, l)
	ch <- metric.MustCreate(memoryDesc, float64(host.Memory), l)
	c.collectCPUMetrics(h, ch, l)

	statPath := fmt.Sprintf("hosts/%s/statistics", host.ID)
	statistic.CollectMetrics(statPath, prefix, labelNames, l, c.client, ch)

	if c.collectNetwork {
		networkPath := fmt.Sprintf("hosts/%s/nics", host.ID)
		network.CollectMetricsForHost(networkPath, prefix, labelNames, l, c.client, ch)
	}
}

func (c *HostCollector) collectCPUMetrics(host *Host, ch chan prometheus.Metric, l []string) {
	topo := host.CPU.Topology
	ch <- metric.MustCreate(cpuCoresDesc, float64(topo.Cores), l)
	ch <- metric.MustCreate(cpuThreadsDesc, float64(topo.Threads), l)
	ch <- metric.MustCreate(cpuSocketsDesc, float64(topo.Sockets), l)
	ch <- metric.MustCreate(cpuSpeedDesc, float64(host.CPU.Speed*1e6), l)
}

func (c *HostCollector) addMetric(desc *prometheus.Desc, v float64, labelValues []string) {
	c.metrics = append(c.metrics, prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, labelValues...))
}

func (c *HostCollector) upMetric(h *Host, labelValues []string) prometheus.Metric {
	var up float64
	if h.Status == "up" {
		up = 1
	}

	return metric.MustCreate(upDesc, up, labelValues)
}
