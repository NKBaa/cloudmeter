package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

type hostMetricSample struct {
	CPUPercent                                                                                         float64
	MemoryTotal, MemoryUsed, MemoryAvailable, DiskTotal, DiskUsed, DiskAvailable, NetworkRX, NetworkTX int64
	CPUErr, MemoryErr, DiskErr, NetworkErr                                                             string
}

var previousCPUIdle, previousCPUTotal uint64
var previousNetworkRX, previousNetworkTX int64
var previousNetworkAt time.Time

func readHostMetrics() hostMetricSample {
	var s hostMetricSample
	f, e := os.Open("/host/proc/stat")
	if e != nil {
		s.CPUErr = e.Error()
	} else {
		scan := bufio.NewScanner(f)
		if scan.Scan() {
			v := strings.Fields(scan.Text())
			if len(v) >= 5 && v[0] == "cpu" {
				var total uint64
				for _, x := range v[1:] {
					n, _ := strconv.ParseUint(x, 10, 64)
					total += n
				}
				idle, _ := strconv.ParseUint(v[4], 10, 64)
				if len(v) > 5 {
					n, _ := strconv.ParseUint(v[5], 10, 64)
					idle += n
				}
				if previousCPUTotal > 0 && total > previousCPUTotal {
					s.CPUPercent = float64((total-previousCPUTotal)-(idle-previousCPUIdle)) * 100 / float64(total-previousCPUTotal)
				}
				previousCPUIdle, previousCPUTotal = idle, total
			} else {
				s.CPUErr = "invalid /proc/stat"
			}
		}
		f.Close()
	}
	memory, e := os.ReadFile("/host/proc/meminfo")
	if e != nil {
		s.MemoryErr = e.Error()
	} else {
		values := map[string]int64{}
		scan := bufio.NewScanner(strings.NewReader(string(memory)))
		for scan.Scan() {
			p := strings.Fields(scan.Text())
			if len(p) >= 2 {
				n, _ := strconv.ParseInt(p[1], 10, 64)
				values[strings.TrimSuffix(p[0], ":")] = n * 1024
			}
		}
		s.MemoryTotal = values["MemTotal"]
		s.MemoryAvailable = values["MemAvailable"]
		s.MemoryUsed = s.MemoryTotal - s.MemoryAvailable
		if s.MemoryTotal <= 0 {
			s.MemoryErr = "invalid /proc/meminfo"
		}
	}
	s.DiskTotal, s.DiskAvailable, e = hostDiskUsage("/host/docker-data")
	if e != nil {
		s.DiskErr = e.Error()
	} else {
		s.DiskUsed = s.DiskTotal - s.DiskAvailable
	}
	network, e := os.Open("/host/proc/net/dev")
	if e != nil {
		s.NetworkErr = e.Error()
	} else {
		scan := bufio.NewScanner(network)
		for scan.Scan() {
			line := strings.TrimSpace(scan.Text())
			name, data, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(name) == "lo" {
				continue
			}
			p := strings.Fields(data)
			if len(p) >= 9 {
				rx, _ := strconv.ParseInt(p[0], 10, 64)
				tx, _ := strconv.ParseInt(p[8], 10, 64)
				s.NetworkRX += rx
				s.NetworkTX += tx
			}
		}
		network.Close()
	}
	return s
}
func hostMetricsRates(s hostMetricSample) (float64, float64) {
	now := time.Now()
	if previousNetworkAt.IsZero() {
		previousNetworkRX, previousNetworkTX, previousNetworkAt = s.NetworkRX, s.NetworkTX, now
		return 0, 0
	}
	seconds := now.Sub(previousNetworkAt).Seconds()
	rx, tx := float64(s.NetworkRX-previousNetworkRX)/seconds, float64(s.NetworkTX-previousNetworkTX)/seconds
	previousNetworkRX, previousNetworkTX, previousNetworkAt = s.NetworkRX, s.NetworkTX, now
	if rx < 0 {
		rx = 0
	}
	if tx < 0 {
		tx = 0
	}
	return rx, tx
}
