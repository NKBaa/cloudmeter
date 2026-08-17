package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DockerExecutor talks to the local Engine over a Unix socket. The worker is
// the only service granted this socket; user containers never receive it.
type DockerExecutor struct {
	client *http.Client
	socket string
	owner  string
}

func NewDockerExecutor(socket, owner string) *DockerExecutor {
	if strings.TrimSpace(socket) == "" {
		socket = "/var/run/docker.sock"
	}
	owner = strings.TrimSpace(owner)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &DockerExecutor{socket: socket, owner: owner, client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socket)
	}}, Timeout: 5 * time.Minute}}
}

func (e *DockerExecutor) Ping(ctx context.Context) error {
	return e.request(ctx, http.MethodGet, "/_ping", nil, nil)
}

type containerCreate struct {
	Image            string            `json:"Image"`
	Env              []string          `json:"Env,omitempty"`
	Cmd              []string          `json:"Cmd,omitempty"`
	Labels           map[string]string `json:"Labels,omitempty"`
	HostConfig       map[string]any    `json:"HostConfig,omitempty"`
	NetworkingConfig map[string]any    `json:"NetworkingConfig,omitempty"`
}

func (e *DockerExecutor) ContainerNames(ctx context.Context, namePrefix string) ([]string, error) {
	filters, err := json.Marshal(map[string][]string{"name": {namePrefix}})
	if err != nil {
		return nil, err
	}
	var items []struct {
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
	}
	if err = e.request(ctx, http.MethodGet, "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil, &items); err != nil {
		return nil, err
	}
	names := []string{}
	for _, item := range items {
		if !e.ownsLabels(item.Labels) {
			continue
		}
		for _, name := range item.Names {
			name = strings.TrimPrefix(name, "/")
			if strings.HasPrefix(name, namePrefix) {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// NetworkNames returns Docker networks whose names begin with the supplied
// prefix. The worker uses this to reclaim test networks left behind if it
// stops between network creation and test-container creation.
func (e *DockerExecutor) NetworkNames(ctx context.Context, namePrefix string) ([]string, error) {
	filters, err := json.Marshal(map[string][]string{"name": {namePrefix}})
	if err != nil {
		return nil, err
	}
	var items []struct {
		Name   string            `json:"Name"`
		Labels map[string]string `json:"Labels"`
	}
	if err = e.request(ctx, http.MethodGet, "/networks?filters="+url.QueryEscape(string(filters)), nil, &items); err != nil {
		return nil, err
	}
	names := []string{}
	for _, item := range items {
		if !e.ownsLabels(item.Labels) {
			continue
		}
		if strings.HasPrefix(item.Name, namePrefix) {
			names = append(names, item.Name)
		}
	}
	return names, nil
}

func (e *DockerExecutor) ownsLabels(labels map[string]string) bool {
	if e.owner == "" {
		return true
	}
	owner := labels["cloudmeter.owner"]
	if owner == e.owner {
		return true
	}
	// Older production resources did not carry an owner label. Only the
	// production owner may keep operating them; isolated stacks never adopt
	// unlabeled Engine resources.
	return owner == "" && UsesLegacyResourceNames(e.owner)
}

func (e *DockerExecutor) assertContainerOwner(ctx context.Context, name string) error {
	if e.owner == "" {
		return nil
	}
	var out struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := e.request(ctx, http.MethodGet, "/containers/"+urlEscape(name)+"/json", nil, &out); err != nil {
		return err
	}
	if !e.ownsLabels(out.Config.Labels) {
		return fmt.Errorf("docker container %s belongs to a different runtime owner", name)
	}
	return nil
}

func (e *DockerExecutor) assertNetworkOwner(ctx context.Context, name string) error {
	if e.owner == "" {
		return nil
	}
	var out struct {
		Labels map[string]string `json:"Labels"`
	}
	if err := e.request(ctx, http.MethodGet, "/networks/"+urlEscape(name), nil, &out); err != nil {
		return err
	}
	if !e.ownsLabels(out.Labels) {
		return fmt.Errorf("docker network %s belongs to a different runtime owner", name)
	}
	return nil
}

func (e *DockerExecutor) assertVolumeOwner(ctx context.Context, name string) error {
	if e.owner == "" {
		return nil
	}
	var out struct {
		Labels map[string]string `json:"Labels"`
	}
	if err := e.request(ctx, http.MethodGet, "/volumes/"+urlEscape(name), nil, &out); err != nil {
		return err
	}
	if !e.ownsLabels(out.Labels) {
		return fmt.Errorf("docker volume %s belongs to a different runtime owner", name)
	}
	return nil
}

func (e *DockerExecutor) request(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("docker engine %s %s: %s", method, path, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (e *DockerExecutor) EnsureNetwork(ctx context.Context, name string) error {
	var out struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	err := e.request(ctx, http.MethodGet, "/networks/"+name, nil, &out)
	if err == nil {
		if !e.ownsLabels(out.Labels) {
			return fmt.Errorf("docker network %s belongs to a different runtime owner", name)
		}
		return nil
	}
	if !isDockerNotFound(err) {
		return err
	}
	return e.request(ctx, http.MethodPost, "/networks/create", map[string]any{"Name": name, "Driver": "bridge", "Internal": true, "Labels": e.managedLabels(nil), "Options": map[string]string{"com.docker.network.bridge.enable_icc": "true"}}, &out)
}

func (e *DockerExecutor) managedLabels(extra map[string]string) map[string]string {
	labels := map[string]string{"cloudmeter.managed": "true"}
	if e.owner != "" {
		labels["cloudmeter.owner"] = e.owner
	}
	for key, value := range extra {
		labels[key] = value
	}
	return labels
}

func (e *DockerExecutor) ConnectNetwork(ctx context.Context, network, container string, aliases ...string) error {
	if err := e.assertNetworkOwner(ctx, network); err != nil {
		return err
	}
	if err := e.assertContainerOwner(ctx, container); err != nil {
		return err
	}
	endpoint := map[string]any{}
	if len(aliases) > 0 {
		endpoint["Aliases"] = aliases
	}
	err := e.request(ctx, http.MethodPost, "/networks/"+urlEscape(network)+"/connect", map[string]any{"Container": container, "EndpointConfig": endpoint}, nil)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return err
	}
	return nil
}

func (e *DockerExecutor) DisconnectNetwork(ctx context.Context, network, container string) error {
	if err := e.assertNetworkOwner(ctx, network); err != nil {
		if !isDockerNotFound(err) {
			return err
		}
		return nil
	}
	if err := e.assertContainerOwner(ctx, container); err != nil {
		if !isDockerNotFound(err) {
			return err
		}
		return nil
	}
	err := e.request(ctx, http.MethodPost, "/networks/"+urlEscape(network)+"/disconnect", map[string]any{"Container": container, "Force": true}, nil)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not connected") && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	return nil
}

func (e *DockerExecutor) RemoveNetwork(ctx context.Context, network string) error {
	if err := e.assertNetworkOwner(ctx, network); err != nil {
		if isDockerNotFound(err) {
			return nil
		}
		return err
	}
	err := e.request(ctx, http.MethodDelete, "/networks/"+urlEscape(network), nil, nil)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	return nil
}

func (e *DockerExecutor) Pull(ctx context.Context, image string) error {
	var metadata map[string]any
	if err := e.request(ctx, http.MethodGet, "/images/"+urlEscape(image)+"/json", nil, &metadata); err == nil {
		return nil
	}
	return e.request(ctx, http.MethodPost, "/images/create?fromImage="+url.QueryEscape(image), nil, nil)
}

func (e *DockerExecutor) Create(ctx context.Context, name, image, network string, aliases []string, spec map[string]any) error {
	if err := e.assertNetworkOwner(ctx, network); err != nil {
		return err
	}
	env := []string{}
	if values, ok := spec["env"].(map[string]any); ok {
		for k, v := range values {
			env = append(env, k+"="+fmt.Sprint(v))
		}
	}
	hostConfig := map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}, "SecurityOpt": []string{"no-new-privileges:true"}}
	resources, err := RuntimeResources(spec, false)
	if err != nil {
		return err
	}
	hostConfig["Memory"] = int64(resources.MemoryMiB * 1024 * 1024)
	hostConfig["NanoCpus"] = int64(resources.CPUCores * 1_000_000_000)
	storage, err := RuntimeStorage(spec, false)
	if err != nil {
		return err
	}
	hostConfig["StorageOpt"] = map[string]string{"size": fmt.Sprintf("%gG", storage.SystemDiskGiB)}
	binds := []string{}
	for _, mount := range VolumeMounts(spec) {
		volume := AppVolumeNameForOwner(e.owner, fmt.Sprint(spec["appId"]), mount.Key)
		if err := e.EnsureVolume(ctx, volume); err != nil {
			return err
		}
		binds = append(binds, volume+":"+mount.MountPath)
	}
	if len(binds) > 0 {
		hostConfig["Binds"] = binds
	}
	command, err := RuntimeCommand(spec)
	if err != nil {
		return err
	}
	endpoint := map[string]any{}
	if len(aliases) > 0 {
		endpoint["Aliases"] = aliases
	}
	body := containerCreate{Image: image, Env: env, Cmd: command, Labels: e.managedLabels(map[string]string{"cloudmeter.app_id": fmt.Sprint(spec["appId"])}), HostConfig: hostConfig, NetworkingConfig: map[string]any{"EndpointsConfig": map[string]any{network: endpoint}}}
	var out struct {
		ID string `json:"Id"`
	}
	return e.request(ctx, http.MethodPost, "/containers/create?name="+urlEscape(name), body, &out)
}

// CreateProductTest starts from the same immutable runtime specification as a
// user release, but maps declared data volumes to disposable tmpfs mounts.
func (e *DockerExecutor) CreateProductTest(ctx context.Context, name, image, network string, aliases []string, spec map[string]any) error {
	if err := e.assertNetworkOwner(ctx, network); err != nil {
		return err
	}
	env := []string{}
	if values, ok := spec["env"].(map[string]any); ok {
		for key, value := range values {
			env = append(env, key+"="+fmt.Sprint(value))
		}
	}
	resources, err := RuntimeResources(spec, false)
	if err != nil {
		return err
	}
	storage, err := RuntimeStorage(spec, false)
	if err != nil {
		return err
	}
	command, err := RuntimeCommand(spec)
	if err != nil {
		return err
	}
	hostConfig := map[string]any{
		"RestartPolicy": map[string]any{"Name": "no"},
		"SecurityOpt":   []string{"no-new-privileges:true"},
		"Memory":        int64(resources.MemoryMiB * 1024 * 1024),
		"NanoCpus":      int64(resources.CPUCores * 1_000_000_000),
		"StorageOpt":    map[string]string{"size": fmt.Sprintf("%gG", storage.SystemDiskGiB)},
	}
	tmpfs := map[string]string{}
	for _, mount := range VolumeMounts(spec) {
		bytes := int64(mount.SizeGiB * 1024 * 1024 * 1024)
		tmpfs[mount.MountPath] = fmt.Sprintf("rw,nosuid,nodev,size=%d", bytes)
	}
	if len(tmpfs) > 0 {
		hostConfig["Tmpfs"] = tmpfs
	}
	endpoint := map[string]any{}
	if len(aliases) > 0 {
		endpoint["Aliases"] = aliases
	}
	body := containerCreate{
		Image: image, Env: env, Cmd: command,
		Labels:           e.managedLabels(map[string]string{"cloudmeter.product_test_id": fmt.Sprint(spec["testId"])}),
		HostConfig:       hostConfig,
		NetworkingConfig: map[string]any{"EndpointsConfig": map[string]any{network: endpoint}},
	}
	var out struct {
		ID string `json:"Id"`
	}
	return e.request(ctx, http.MethodPost, "/containers/create?name="+urlEscape(name), body, &out)
}

func (e *DockerExecutor) Start(ctx context.Context, name string) error {
	if err := e.assertContainerOwner(ctx, name); err != nil {
		return err
	}
	return e.request(ctx, http.MethodPost, "/containers/"+urlEscape(name)+"/start", nil, nil)
}
func (e *DockerExecutor) Stop(ctx context.Context, name string) error {
	if err := e.assertContainerOwner(ctx, name); err != nil {
		return err
	}
	return e.request(ctx, http.MethodPost, "/containers/"+urlEscape(name)+"/stop?t=20", nil, nil)
}
func (e *DockerExecutor) StopIfExists(ctx context.Context, name string) error {
	err := e.Stop(ctx, name)
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such container") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "container already stopped") ||
		strings.Contains(message, "not modified") {
		return nil
	}
	return err
}
func (e *DockerExecutor) Remove(ctx context.Context, name string) error {
	if err := e.assertContainerOwner(ctx, name); err != nil {
		return err
	}
	return e.request(ctx, http.MethodDelete, "/containers/"+urlEscape(name)+"?force=true", nil, nil)
}
func (e *DockerExecutor) RemoveIfExists(ctx context.Context, name string) error {
	err := e.Remove(ctx, name)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such container") && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	return nil
}
func (e *DockerExecutor) EnsureVolume(ctx context.Context, name string) error {
	var out struct {
		Labels map[string]string `json:"Labels"`
	}
	err := e.request(ctx, http.MethodGet, "/volumes/"+urlEscape(name), nil, &out)
	if err == nil {
		if !e.ownsLabels(out.Labels) {
			return fmt.Errorf("docker volume %s belongs to a different runtime owner", name)
		}
		return nil
	}
	if !isDockerNotFound(err) {
		return err
	}
	return e.request(ctx, http.MethodPost, "/volumes/create", map[string]any{"Name": name, "Labels": e.managedLabels(nil)}, &out)
}

// VolumeSize returns the Engine-reported byte usage for a managed volume.
func (e *DockerExecutor) VolumeSize(ctx context.Context, name string) (int64, error) {
	if err := e.assertVolumeOwner(ctx, name); err != nil {
		return 0, err
	}
	var out struct {
		UsageData *struct {
			Size int64 `json:"Size"`
		} `json:"UsageData"`
	}
	if err := e.request(ctx, http.MethodGet, "/volumes/"+urlEscape(name), nil, &out); err != nil {
		return 0, err
	}
	if out.UsageData == nil || out.UsageData.Size < 0 {
		return 0, nil
	}
	return out.UsageData.Size, nil
}

func (e *DockerExecutor) ArchiveVolume(ctx context.Context, helperImage, backupVolume, sourceVolume, storageKey, jobID string) (int64, error) {
	if err := e.EnsureVolume(ctx, backupVolume); err != nil {
		return 0, err
	}
	if err := e.assertVolumeOwner(ctx, sourceVolume); err != nil {
		return 0, err
	}
	cmd := []string{"/bin/sh", "-c", "tar -czf /backup/" + storageKey + " -C /source . && stat -c %s /backup/" + storageKey}
	output, err := e.runHelperOutput(ctx, HelperContainerName(e.owner, "backup", jobID), helperImage, cmd, []string{sourceVolume + ":/source:ro", backupVolume + ":/backup"})
	if err != nil {
		return 0, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("backup size output is invalid")
	}
	return size, nil
}
func (e *DockerExecutor) DeleteBackup(ctx context.Context, helperImage, backupVolume, storageKey, jobID string) error {
	if err := e.assertVolumeOwner(ctx, backupVolume); err != nil {
		return err
	}
	cmd := []string{"/bin/sh", "-c", "rm -f /backup/" + storageKey}
	return e.runHelper(ctx, HelperContainerName(e.owner, "backup-delete", jobID), helperImage, cmd, []string{backupVolume + ":/backup"})
}
func (e *DockerExecutor) RestoreVolume(ctx context.Context, helperImage, backupVolume, targetVolume, storageKey, jobID string) error {
	if err := e.assertVolumeOwner(ctx, backupVolume); err != nil {
		return err
	}
	if err := e.assertVolumeOwner(ctx, targetVolume); err != nil {
		return err
	}
	cmd := []string{"/bin/sh", "-c", "find /target -mindepth 1 -maxdepth 1 -exec rm -rf {} + && tar -xzf /backup/" + storageKey + " -C /target"}
	return e.runHelper(ctx, HelperContainerName(e.owner, "restore", jobID), helperImage, cmd, []string{targetVolume + ":/target", backupVolume + ":/backup:ro"})
}
func (e *DockerExecutor) runHelper(ctx context.Context, name, image string, cmd, binds []string) error {
	_, err := e.runHelperOutput(ctx, name, image, cmd, binds)
	return err
}
func (e *DockerExecutor) runHelperOutput(ctx context.Context, name, image string, cmd, binds []string) (string, error) {
	body := containerCreate{Image: image, Cmd: cmd, Labels: e.managedLabels(map[string]string{"cloudmeter.helper": "true"}), HostConfig: map[string]any{"Binds": binds, "NetworkMode": "none", "SecurityOpt": []string{"no-new-privileges:true"}}}
	var created struct {
		ID string `json:"Id"`
	}
	if err := e.request(ctx, http.MethodPost, "/containers/create?name="+urlEscape(name), body, &created); err != nil {
		return "", err
	}
	defer e.Remove(context.Background(), name)
	if err := e.Start(ctx, name); err != nil {
		return "", err
	}
	var waited struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := e.request(ctx, http.MethodPost, "/containers/"+urlEscape(name)+"/wait?condition=not-running", nil, &waited); err != nil {
		return "", err
	}
	if waited.StatusCode != 0 {
		message := "helper exited unsuccessfully"
		if waited.Error != nil && waited.Error.Message != "" {
			message = waited.Error.Message
		}
		return "", fmt.Errorf("%s: %s", message, name)
	}
	data, err := e.rawRequest(ctx, http.MethodGet, "/containers/"+urlEscape(name)+"/logs?stdout=true&stderr=true", nil)
	if err != nil {
		return "", err
	}
	return decodeDockerLogs(data), nil
}

func (e *DockerExecutor) ProbeHTTP(ctx context.Context, name, image, network, target string, timeoutSeconds int) error {
	if timeoutSeconds < 1 {
		timeoutSeconds = 5
	}
	if err := e.assertNetworkOwner(ctx, network); err != nil {
		return err
	}
	// A worker may stop after creating the helper but before its deferred
	// cleanup runs. Reclaim the deterministic name before retrying the probe.
	if err := e.RemoveIfExists(ctx, name); err != nil {
		return err
	}
	cmd := []string{"wget", "-q", "-T", strconv.Itoa(timeoutSeconds), "-O", "/dev/null", "--", target}
	body := containerCreate{
		Image:  image,
		Cmd:    cmd,
		Labels: e.managedLabels(map[string]string{"cloudmeter.health_probe": "true"}),
		HostConfig: map[string]any{
			"NetworkMode": network,
			"SecurityOpt": []string{"no-new-privileges:true"},
			"CapDrop":     []string{"ALL"},
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := e.request(ctx, http.MethodPost, "/containers/create?name="+urlEscape(name), body, &created); err != nil {
		return err
	}
	defer e.Remove(context.Background(), name)
	if err := e.Start(ctx, name); err != nil {
		return err
	}
	var waited struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := e.request(ctx, http.MethodPost, "/containers/"+urlEscape(name)+"/wait?condition=not-running", nil, &waited); err != nil {
		return err
	}
	if waited.StatusCode != 0 {
		return fmt.Errorf("HTTP health probe failed with exit code %d", waited.StatusCode)
	}
	return nil
}

func (e *DockerExecutor) rawRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, reader)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("docker engine %s %s: %s", method, path, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func decodeDockerLogs(data []byte) string {
	var output bytes.Buffer
	for len(data) >= 8 {
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		if size < 0 || len(data) < 8+size {
			return string(data)
		}
		output.Write(data[8 : 8+size])
		data = data[8+size:]
	}
	if output.Len() == 0 {
		return string(data)
	}
	return output.String()
}

func (e *DockerExecutor) Healthy(ctx context.Context, name string) (bool, error) {
	if err := e.assertContainerOwner(ctx, name); err != nil {
		return false, err
	}
	var out struct {
		State struct {
			Running    bool      `json:"Running"`
			Restarting bool      `json:"Restarting"`
			StartedAt  time.Time `json:"StartedAt"`
			Health     *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
	}
	if err := e.request(ctx, http.MethodGet, "/containers/"+urlEscape(name)+"/json", nil, &out); err != nil {
		return false, err
	}
	if !out.State.Running || out.State.Restarting || time.Since(out.State.StartedAt) < 3*time.Second {
		return false, nil
	}
	if out.State.Health != nil {
		return out.State.Health.Status == "healthy", nil
	}
	return true, nil
}

// Stats returns a point-in-time CPU core estimate and memory usage.
func (e *DockerExecutor) Stats(ctx context.Context, name string) (float64, int64, error) {
	if err := e.assertContainerOwner(ctx, name); err != nil {
		return 0, 0, err
	}
	var out struct {
		CPUStats struct {
			CPUUsage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
			Online uint64 `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		Memory struct {
			Usage uint64 `json:"usage"`
		} `json:"memory_stats"`
	}
	if err := e.request(ctx, http.MethodGet, `/containers/`+urlEscape(name)+`/stats?stream=false`, nil, &out); err != nil {
		return 0, 0, err
	}
	if out.CPUStats.CPUUsage.Total < out.PreCPUStats.CPUUsage.Total || out.CPUStats.System < out.PreCPUStats.System {
		return 0, int64(out.Memory.Usage), nil
	}
	deltaCPU := out.CPUStats.CPUUsage.Total - out.PreCPUStats.CPUUsage.Total
	deltaSystem := out.CPUStats.System - out.PreCPUStats.System
	if deltaSystem == 0 {
		return 0, int64(out.Memory.Usage), nil
	}
	cores := out.CPUStats.Online
	if cores == 0 {
		cores = 1
	}
	return float64(deltaCPU) / float64(deltaSystem) * float64(cores), int64(out.Memory.Usage), nil
}

func urlEscape(value string) string {
	return strings.NewReplacer("/", "%2F", " ", "%20", "?", "%3F", "#", "%23").Replace(value)
}

func isDockerNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such") || strings.Contains(message, "not found")
}
func numeric(value any) (float64, bool) { v, ok := value.(float64); return v, ok }
