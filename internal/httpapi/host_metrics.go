package httpapi

import "net/http"

func (s *Server) hostMetrics(w http.ResponseWriter, r *http.Request) {
	var cpu *float64
	var memoryTotal, memoryUsed, memoryAvailable, diskTotal, diskUsed, diskAvailable, networkRX, networkTX *int64
	var rxRate, txRate *float64
	var cpuErr, memoryErr, diskErr, networkErr string
	var sampled any
	err := s.db.QueryRow(r.Context(), "SELECT cpu_usage_percent::float8,memory_total_bytes,memory_used_bytes,memory_available_bytes,disk_total_bytes,disk_used_bytes,disk_available_bytes,network_rx_bytes,network_tx_bytes,network_rx_bytes_per_second::float8,network_tx_bytes_per_second::float8,cpu_error,memory_error,disk_error,network_error,sampled_at FROM host_metrics WHERE singleton").Scan(&cpu, &memoryTotal, &memoryUsed, &memoryAvailable, &diskTotal, &diskUsed, &diskAvailable, &networkRX, &networkTX, &rxRate, &txRate, &cpuErr, &memoryErr, &diskErr, &networkErr, &sampled)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"cpu": map[string]any{"usagePercent": cpu, "error": cpuErr}, "memory": map[string]any{"totalBytes": memoryTotal, "usedBytes": memoryUsed, "availableBytes": memoryAvailable, "error": memoryErr}, "disk": map[string]any{"totalBytes": diskTotal, "usedBytes": diskUsed, "availableBytes": diskAvailable, "error": diskErr}, "network": map[string]any{"rxBytes": networkRX, "txBytes": networkTX, "rxBytesPerSecond": rxRate, "txBytesPerSecond": txRate, "error": networkErr}, "sampledAt": sampled})
}
