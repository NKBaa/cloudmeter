package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
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
	}}, Timeout: 30 * time.Minute}}
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

type DockerImage struct {
	ID                  string
	RepoTags            []string
	SizeBytes           int64
	CreatedUnix         int64
	ContainerReferences int
}

func (e *DockerExecutor) Images(ctx context.Context) ([]DockerImage, error) {
	var rawImages []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
		Size     int64    `json:"Size"`
		Created  int64    `json:"Created"`
	}
	if err := e.request(ctx, http.MethodGet, "/images/json?all=false", nil, &rawImages); err != nil {
		return nil, err
	}
	var containers []struct {
		ImageID string `json:"ImageID"`
	}
	if err := e.request(ctx, http.MethodGet, "/containers/json?all=true", nil, &containers); err != nil {
		return nil, err
	}
	references := map[string]int{}
	for _, container := range containers {
		references[container.ImageID]++
	}
	items := make([]DockerImage, 0, len(rawImages))
	for _, image := range rawImages {
		items = append(items, DockerImage{ID: image.ID, RepoTags: image.RepoTags, SizeBytes: image.Size, CreatedUnix: image.Created, ContainerReferences: references[image.ID]})
	}
	return items, nil
}

func (e *DockerExecutor) RemoveImage(ctx context.Context, imageID string) error {
	if !strings.HasPrefix(imageID, "sha256:") || len(imageID) != 71 {
		return fmt.Errorf("invalid docker image ID")
	}
	return e.request(ctx, http.MethodDelete, "/images/"+urlEscape(imageID)+"?force=false&noprune=false", nil, nil)
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
	return e.PullWithRegistry(ctx, image, "", "", "")
}

func (e *DockerExecutor) PullWithRegistry(ctx context.Context, image, username, password, serverAddress string) error {
	var metadata map[string]any
	if err := e.request(ctx, http.MethodGet, "/images/"+urlEscape(image)+"/json", nil, &metadata); err == nil {
		return nil
	}
	path := "/images/create?fromImage=" + url.QueryEscape(image)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	if username != "" || password != "" {
		auth, marshalErr := json.Marshal(map[string]string{
			"username": username, "password": password, "serveraddress": serverAddress,
		})
		if marshalErr != nil {
			return marshalErr
		}
		// Docker's X-Registry-Auth header is ordinary RFC 4648 Base64.
		// RawURLEncoding is subtly different (it replaces '+'/'/' and removes
		// padding), which makes private-registry pulls fail even when the
		// configured credentials are correct.
		req.Header.Set("X-Registry-Auth", base64.StdEncoding.EncodeToString(auth))
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker image pull %s: %w", image, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("docker engine POST %s: %s", path, strings.TrimSpace(string(data)))
	}
	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err = decoder.Decode(&event); err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("docker image pull %s response: %w", image, err)
		}
		if event.Error != "" {
			return fmt.Errorf("docker image pull %s: %s", image, event.Error)
		}
		if event.ErrorDetail.Message != "" {
			return fmt.Errorf("docker image pull %s: %s", image, event.ErrorDetail.Message)
		}
	}
	metadata = nil
	if err = e.request(ctx, http.MethodGet, "/images/"+urlEscape(image)+"/json", nil, &metadata); err != nil {
		return fmt.Errorf("docker image pull completed but image %s is unavailable: %w", image, err)
	}
	return nil
}

type DockerDaemonSettings struct {
	RegistryMirrors []string
	HTTPProxy       string
	HTTPSProxy      string
	NoProxy         string
}

func (e *DockerExecutor) DaemonSettings(ctx context.Context) (DockerDaemonSettings, error) {
	var out struct {
		RegistryConfig struct {
			Mirrors []string `json:"Mirrors"`
		} `json:"RegistryConfig"`
		HTTPProxy  string `json:"HttpProxy"`
		HTTPSProxy string `json:"HttpsProxy"`
		NoProxy    string `json:"NoProxy"`
	}
	if err := e.request(ctx, http.MethodGet, "/info", nil, &out); err != nil {
		return DockerDaemonSettings{}, err
	}
	return DockerDaemonSettings{
		RegistryMirrors: out.RegistryConfig.Mirrors, HTTPProxy: out.HTTPProxy, HTTPSProxy: out.HTTPSProxy, NoProxy: out.NoProxy,
	}, nil
}

var restartableComposeServices = map[string]struct{}{
	"gateway": {}, "web": {}, "api": {}, "app-router": {}, "egress-proxy": {}, "worker": {},
}

// RestartComposeService restarts one fixed CloudMeter control-plane service.
// The service name is allow-listed and Docker labels are checked again after
// filtering, so callers cannot turn this into a general container restart
// primitive or cross Compose project boundaries.
func (e *DockerExecutor) RestartComposeService(ctx context.Context, service string, waitReady bool) error {
	service = strings.TrimSpace(service)
	if _, allowed := restartableComposeServices[service]; !allowed {
		return fmt.Errorf("compose service %q is not restartable", service)
	}
	if strings.TrimSpace(e.owner) == "" {
		return fmt.Errorf("runtime owner is required for platform restart")
	}
	filters, err := json.Marshal(map[string][]string{"label": {
		"com.docker.compose.project=" + e.owner,
		"com.docker.compose.service=" + service,
		"cloudmeter.owner=" + e.owner,
	}})
	if err != nil {
		return err
	}
	var containers []struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	if err = e.request(ctx, http.MethodGet, "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil, &containers); err != nil {
		return fmt.Errorf("find compose service %s: %w", service, err)
	}
	matches := make([]string, 0, 1)
	for _, container := range containers {
		if container.ID == "" ||
			container.Labels["com.docker.compose.project"] != e.owner ||
			container.Labels["com.docker.compose.service"] != service ||
			container.Labels["cloudmeter.owner"] != e.owner {
			continue
		}
		matches = append(matches, container.ID)
	}
	if len(matches) != 1 {
		return fmt.Errorf("compose service %s expected exactly one owned container, found %d", service, len(matches))
	}
	containerID := matches[0]
	if err = e.request(ctx, http.MethodPost, "/containers/"+urlEscape(containerID)+"/restart?t=10", nil, nil); err != nil {
		return fmt.Errorf("restart compose service %s: %w", service, err)
	}
	if !waitReady {
		return nil
	}
	return e.waitComposeServiceReady(ctx, containerID, service)
}

func (e *DockerExecutor) waitComposeServiceReady(ctx context.Context, containerID, service string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var out struct {
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
			State struct {
				Running    bool `json:"Running"`
				Restarting bool `json:"Restarting"`
				Health     *struct {
					Status string `json:"Status"`
				} `json:"Health"`
			} `json:"State"`
		}
		err := e.request(ctx, http.MethodGet, "/containers/"+urlEscape(containerID)+"/json", nil, &out)
		if err == nil {
			labels := out.Config.Labels
			if labels["com.docker.compose.project"] != e.owner || labels["com.docker.compose.service"] != service || labels["cloudmeter.owner"] != e.owner {
				return fmt.Errorf("compose service %s labels changed during restart", service)
			}
			if out.State.Running && !out.State.Restarting && (out.State.Health == nil || out.State.Health.Status == "healthy") {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("compose service %s did not become ready: %w", service, ctx.Err())
		case <-ticker.C:
		}
	}
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
func (e *DockerExecutor) RemoveVolumeIfExists(ctx context.Context, name string) error {
	if err := e.assertVolumeOwner(ctx, name); err != nil {
		if isDockerNotFound(err) {
			return nil
		}
		return err
	}
	err := e.request(ctx, http.MethodDelete, "/volumes/"+urlEscape(name)+"?force=true", nil, nil)
	if err != nil && !isDockerNotFound(err) {
		return err
	}
	return nil
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
	if out.UsageData != nil && out.UsageData.Size >= 0 {
		return out.UsageData.Size, nil
	}
	// Some Engine versions omit UsageData from volume inspect and expose it
	// only through system disk usage. Use that endpoint before giving up so
	// shared application-volume quotas are based on observed bytes.
	var diskUsage struct {
		Volumes []struct {
			Name      string            `json:"Name"`
			Labels    map[string]string `json:"Labels"`
			UsageData *struct {
				Size int64 `json:"Size"`
			} `json:"UsageData"`
		} `json:"Volumes"`
	}
	if err := e.request(ctx, http.MethodGet, "/system/df?type=volume", nil, &diskUsage); err != nil {
		return 0, err
	}
	for _, volume := range diskUsage.Volumes {
		if volume.Name != name {
			continue
		}
		if !e.ownsLabels(volume.Labels) {
			return 0, fmt.Errorf("docker volume %s belongs to a different runtime owner", name)
		}
		if volume.UsageData == nil || volume.UsageData.Size < 0 {
			return 0, fmt.Errorf("docker engine did not report usage for volume %s", name)
		}
		return volume.UsageData.Size, nil
	}
	return 0, fmt.Errorf("docker engine did not return volume %s in disk usage", name)
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
	name := HelperContainerName(e.owner, "backup-delete", jobID)
	if err := e.RemoveIfExists(ctx, name); err != nil {
		return err
	}
	cmd := []string{"/bin/sh", "-c", "rm -f /backup/" + storageKey}
	return e.runHelper(ctx, name, helperImage, cmd, []string{backupVolume + ":/backup"})
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

func (e *DockerExecutor) ProbeHTTP(ctx context.Context, name, image, network, target string, timeoutSeconds int, acceptedStatusCodes []int) error {
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
	cmd := []string{"wget", "-S", "-T", strconv.Itoa(timeoutSeconds), "-O", "/dev/null", "--", target}
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
		data, logErr := e.rawRequest(ctx, http.MethodGet, "/containers/"+urlEscape(name)+"/logs?stdout=true&stderr=true&tail=40", nil)
		detail := ""
		if logErr == nil {
			detail = strings.TrimSpace(decodeDockerLogs(data))
		}
		if statusCode, ok := lastHTTPStatusCode(detail); ok && containsStatusCode(acceptedStatusCodes, statusCode) {
			return nil
		}
		if len(detail) > 1200 {
			detail = detail[len(detail)-1200:]
		}
		if detail != "" {
			return fmt.Errorf("HTTP health probe failed with exit code %d: %s", waited.StatusCode, detail)
		}
		return fmt.Errorf("HTTP health probe failed with exit code %d", waited.StatusCode)
	}
	return nil
}

var httpStatusLine = regexp.MustCompile(`(?i)HTTP/[0-9.]+[ \t]+([0-9]{3})(?:[ \t]|$)`)

func lastHTTPStatusCode(value string) (int, bool) {
	matches := httpStatusLine.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return 0, false
	}
	statusCode, err := strconv.Atoi(matches[len(matches)-1][1])
	return statusCode, err == nil
}

func containsStatusCode(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (e *DockerExecutor) ContainerDiagnostics(ctx context.Context, name string, tail int) (string, error) {
	if err := e.assertContainerOwner(ctx, name); err != nil {
		return "", err
	}
	if tail < 1 || tail > 500 {
		tail = 80
	}
	var inspected struct {
		State struct {
			Status     string `json:"Status"`
			Running    bool   `json:"Running"`
			Restarting bool   `json:"Restarting"`
			OOMKilled  bool   `json:"OOMKilled"`
			ExitCode   int    `json:"ExitCode"`
			Error      string `json:"Error"`
		} `json:"State"`
	}
	if err := e.request(ctx, http.MethodGet, "/containers/"+urlEscape(name)+"/json", nil, &inspected); err != nil {
		return "", err
	}
	data, err := e.rawRequest(ctx, http.MethodGet, "/containers/"+urlEscape(name)+"/logs?stdout=true&stderr=true&tail="+strconv.Itoa(tail), nil)
	if err != nil {
		return "", err
	}
	state := fmt.Sprintf("状态=%s running=%t restarting=%t oomKilled=%t exitCode=%d", inspected.State.Status, inspected.State.Running, inspected.State.Restarting, inspected.State.OOMKilled, inspected.State.ExitCode)
	if inspected.State.Error != "" {
		state += " error=" + inspected.State.Error
	}
	logs := strings.TrimSpace(decodeDockerLogs(data))
	if len(logs) > 6000 {
		logs = logs[len(logs)-6000:]
	}
	return state + "\n" + logs, nil
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
			Usage uint64            `json:"usage"`
			Stats map[string]uint64 `json:"stats"`
		} `json:"memory_stats"`
	}
	if err := e.request(ctx, http.MethodGet, `/containers/`+urlEscape(name)+`/stats?stream=false`, nil, &out); err != nil {
		return 0, 0, err
	}
	memoryUsage := out.Memory.Usage
	inactiveFile := out.Memory.Stats["total_inactive_file"]
	if inactiveFile == 0 {
		inactiveFile = out.Memory.Stats["inactive_file"]
	}
	if inactiveFile < memoryUsage {
		memoryUsage -= inactiveFile
	}
	if out.CPUStats.CPUUsage.Total < out.PreCPUStats.CPUUsage.Total || out.CPUStats.System < out.PreCPUStats.System {
		return 0, int64(memoryUsage), nil
	}
	deltaCPU := out.CPUStats.CPUUsage.Total - out.PreCPUStats.CPUUsage.Total
	deltaSystem := out.CPUStats.System - out.PreCPUStats.System
	if deltaSystem == 0 {
		return 0, int64(memoryUsage), nil
	}
	cores := out.CPUStats.Online
	if cores == 0 {
		cores = 1
	}
	return float64(deltaCPU) / float64(deltaSystem) * float64(cores), int64(memoryUsage), nil
}

func urlEscape(value string) string {
	return strings.NewReplacer("/", "%2F", " ", "%20", "?", "%3F", "#", "%23").Replace(value)
}

func isDockerNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such") || strings.Contains(message, "not found")
}
func numeric(value any) (float64, bool) { v, ok := value.(float64); return v, ok }
