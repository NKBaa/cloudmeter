BEGIN;
CREATE TABLE host_metrics (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), cpu_usage_percent numeric(6,3),
 memory_total_bytes bigint, memory_used_bytes bigint, memory_available_bytes bigint,
 disk_total_bytes bigint, disk_used_bytes bigint, disk_available_bytes bigint,
 network_rx_bytes bigint, network_tx_bytes bigint, network_rx_bytes_per_second numeric(30,3), network_tx_bytes_per_second numeric(30,3),
 cpu_error text NOT NULL DEFAULT '', memory_error text NOT NULL DEFAULT '', disk_error text NOT NULL DEFAULT '', network_error text NOT NULL DEFAULT '', sampled_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO host_metrics(singleton) VALUES(true);
COMMIT;
