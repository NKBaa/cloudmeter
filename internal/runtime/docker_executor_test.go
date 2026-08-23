package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestDecodeDockerLogs(t *testing.T) {
	payload := []byte("12345\n")
	frame := append([]byte{1, 0, 0, 0, 0, 0, 0, byte(len(payload))}, payload...)
	if got := decodeDockerLogs(frame); got != string(payload) {
		t.Fatalf("decoded logs = %q", got)
	}
	if got := decodeDockerLogs(payload); got != string(payload) {
		t.Fatalf("plain logs = %q", got)
	}
}

func TestLastHTTPStatusCodeUsesFinalResponse(t *testing.T) {
	logs := "  HTTP/1.1 302 Found\n  Location: /login\n  HTTP/1.1 401 Unauthorized\n"
	statusCode, ok := lastHTTPStatusCode(logs)
	if !ok || statusCode != http.StatusUnauthorized {
		t.Fatalf("last status code=(%d, %v)", statusCode, ok)
	}
	if _, ok = lastHTTPStatusCode("connection refused"); ok {
		t.Fatal("non-HTTP diagnostics produced a status code")
	}
}

func TestContainsStatusCode(t *testing.T) {
	if !containsStatusCode([]int{401, 403}, 401) {
		t.Fatal("declared status code was not accepted")
	}
	if containsStatusCode([]int{401, 403}, 500) {
		t.Fatal("undeclared status code was accepted")
	}
}

func TestStatsReturnsCPUCoresAndMemoryWorkingSet(t *testing.T) {
	requests := 0
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Path != "/containers/cm-test/stats" || request.URL.Query().Get("stream") != "false" {
			t.Fatalf("unexpected stats request %s", request.URL.String())
		}
		body := `{
			"cpu_stats":{"cpu_usage":{"total_usage":300},"system_cpu_usage":1000,"online_cpus":4},
			"precpu_stats":{"cpu_usage":{"total_usage":200},"system_cpu_usage":600},
			"memory_stats":{"usage":314572800,"stats":{"inactive_file":104857600}}
		}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	cpu, memory, err := executor.Stats(context.Background(), "cm-test")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
	if cpu != 1 {
		t.Fatalf("cpu cores=%f", cpu)
	}
	if memory != 200*1024*1024 {
		t.Fatalf("memory working set=%d", memory)
	}
}

func TestConnectNetworkSendsStableAliases(t *testing.T) {
	server, client := net.Pipe()
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		endpoint, ok := body["EndpointConfig"].(map[string]any)
		if !ok {
			t.Fatalf("EndpointConfig=%#v", body["EndpointConfig"])
		}
		aliases, ok := endpoint["Aliases"].([]any)
		if !ok || len(aliases) != 1 || aliases[0] != "cloudmeter-egress-proxy" {
			t.Fatalf("Aliases=%#v", endpoint["Aliases"])
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})}}
	_ = server.Close()
	_ = client.Close()
	if err := executor.ConnectNetwork(context.Background(), "user_net_test", "cloudmeter-egress-proxy-1", "cloudmeter-egress-proxy"); err != nil {
		t.Fatal(err)
	}
}

func TestContainerNamesOnlyReturnsRequestedPrefix(t *testing.T) {
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("filters") == "" {
			t.Fatal("missing Docker filters")
		}
		body := `[{"Names":["/cm-111-222","/other"]}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	names, err := executor.ContainerNames(context.Background(), "cm-")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "cm-111-222" {
		t.Fatalf("names=%v", names)
	}
}

func TestContainerNamesSkipsForeignOwner(t *testing.T) {
	executor := &DockerExecutor{owner: "stack-a", client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body := `[{"Names":["/cm-owned"],"Labels":{"cloudmeter.owner":"stack-a"}},{"Names":["/cm-foreign"],"Labels":{"cloudmeter.owner":"stack-b"}},{"Names":["/cm-legacy"],"Labels":{}}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	names, err := executor.ContainerNames(context.Background(), "cm-")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "cm-owned" {
		t.Fatalf("names=%v", names)
	}
}

func TestRestartComposeServiceOnlyRestartsExactlyOwnedService(t *testing.T) {
	requests := 0
	executor := &DockerExecutor{owner: "stack-a", client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if request.Method != http.MethodGet || request.URL.Path != "/containers/json" || request.URL.Query().Get("all") != "true" {
				t.Fatalf("unexpected lookup request %s %s", request.Method, request.URL.String())
			}
			var filters map[string][]string
			if err := json.Unmarshal([]byte(request.URL.Query().Get("filters")), &filters); err != nil {
				t.Fatal(err)
			}
			wanted := strings.Join(filters["label"], ",")
			if wanted != "com.docker.compose.project=stack-a,com.docker.compose.service=api,cloudmeter.owner=stack-a" {
				t.Fatalf("labels filter=%q", wanted)
			}
			body := `[{"Id":"owned-api","Labels":{"com.docker.compose.project":"stack-a","com.docker.compose.service":"api","cloudmeter.owner":"stack-a"}}]`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/containers/owned-api/restart" || request.URL.Query().Get("t") != "10" {
				t.Fatalf("unexpected restart request %s %s", request.Method, request.URL.String())
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	})}}
	if err := executor.RestartComposeService(context.Background(), "api", false); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestRestartComposeServiceRejectsForeignOrAmbiguousTargets(t *testing.T) {
	for name, body := range map[string]string{
		"foreign owner": `[{"Id":"foreign","Labels":{"com.docker.compose.project":"stack-b","com.docker.compose.service":"api","cloudmeter.owner":"stack-b"}}]`,
		"duplicate":     `[{"Id":"one","Labels":{"com.docker.compose.project":"stack-a","com.docker.compose.service":"api","cloudmeter.owner":"stack-a"}},{"Id":"two","Labels":{"com.docker.compose.project":"stack-a","com.docker.compose.service":"api","cloudmeter.owner":"stack-a"}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			requests := 0
			executor := &DockerExecutor{owner: "stack-a", client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}}
			err := executor.RestartComposeService(context.Background(), "api", false)
			if err == nil || !strings.Contains(err.Error(), "exactly one owned container") {
				t.Fatalf("error=%v", err)
			}
			if requests != 1 {
				t.Fatalf("unsafe restart request count=%d", requests)
			}
		})
	}
}

func TestRestartComposeServiceRejectsServicesOutsideControlPlane(t *testing.T) {
	executor := &DockerExecutor{owner: "stack-a"}
	for _, service := range []string{"postgres", "redis", "migrate", "user-app", ""} {
		if err := executor.RestartComposeService(context.Background(), service, false); err == nil {
			t.Fatalf("service %q was accepted", service)
		}
	}
}

func TestNetworkNamesOnlyReturnsRequestedPrefix(t *testing.T) {
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("filters") == "" {
			t.Fatal("missing Docker filters")
		}
		body := `[{"Name":"cm-test-net-11111111222233334444555555555555"},{"Name":"other-network"}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	names, err := executor.NetworkNames(context.Background(), "cm-test-net-")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "cm-test-net-11111111222233334444555555555555" {
		t.Fatalf("names=%v", names)
	}
}

func TestPullUsesExistingLocalImage(t *testing.T) {
	image := "registry.example.com/team/app@sha256:" + strings.Repeat("a", 64)
	requests := 0
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet {
			t.Fatalf("method=%s", request.Method)
		}
		if request.URL.EscapedPath() != "/images/registry.example.com%2Fteam%2Fapp@sha256:"+strings.Repeat("a", 64)+"/json" {
			t.Fatalf("path=%s", request.URL.EscapedPath())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Id":"local"}`)), Header: make(http.Header)}, nil
	})}}
	if err := executor.Pull(context.Background(), image); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestPullFetchesImageWhenLocalImageIsMissing(t *testing.T) {
	image := "registry.example.com/team/app@sha256:" + strings.Repeat("b", 64)
	requests := 0
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if request.Method != http.MethodGet {
				t.Fatalf("inspect method=%s", request.Method)
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"No such image"}`)), Header: make(http.Header)}, nil
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/images/create" {
				t.Fatalf("pull request=%s %s", request.Method, request.URL.Path)
			}
			if request.URL.Query().Get("fromImage") != image {
				t.Fatalf("fromImage=%q", request.URL.Query().Get("fromImage"))
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{\"status\":\"Pull complete\"}\n")), Header: make(http.Header)}, nil
		case 3:
			if request.Method != http.MethodGet {
				t.Fatalf("verify method=%s", request.Method)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{\"Id\":\"pulled\"}")), Header: make(http.Header)}, nil
		default:
			t.Fatalf("unexpected request %d: %s %s", requests, request.Method, request.URL.String())
			return nil, nil
		}
	})}}
	if err := executor.Pull(context.Background(), image); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestEnsureNetworkOnlyCreatesAfterNotFound(t *testing.T) {
	requests := 0
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != "/networks/user_net_test" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(`{"message":"daemon unavailable"}`)), Header: make(http.Header)}, nil
	})}}
	if err := executor.EnsureNetwork(context.Background(), "user_net_test"); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("EnsureNetwork error=%v", err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d; failed inspect must not create a network", requests)
	}
}

func TestEnsureVolumeOnlyCreatesAfterNotFound(t *testing.T) {
	requests := 0
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != "/volumes/managed_data" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"message":"access denied"}`)), Header: make(http.Header)}, nil
	})}}
	if err := executor.EnsureVolume(context.Background(), "managed_data"); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("EnsureVolume error=%v", err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d; failed inspect must not create a volume", requests)
	}
}

func TestCreatePassesStructuredCommandToDocker(t *testing.T) {
	executor := &DockerExecutor{client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.URL.Path, "/containers/create") {
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
		var body containerCreate
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Cmd) != 3 || body.Cmd[0] != "python" || body.Cmd[2] != "app" {
			t.Fatalf("Cmd=%v", body.Cmd)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"Id":"container"}`)), Header: make(http.Header)}, nil
	})}}
	containerID, err := executor.Create(context.Background(), "test", "example@sha256:"+strings.Repeat("a", 64), "user_net_test", nil, map[string]any{
		"appId": "app-id", "cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0,
		"command": []any{"python", "-m", "app"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if containerID != "container" {
		t.Fatalf("container id=%q want container", containerID)
	}
}

func TestCreateProductTestUsesDisposableStorage(t *testing.T) {
	executor := &DockerExecutor{owner: "stack-a", client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet && request.URL.Path == "/networks/test_net" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Labels":{"cloudmeter.owner":"stack-a"}}`)), Header: make(http.Header)}, nil
		}
		if !strings.HasPrefix(request.URL.Path, "/containers/create") {
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
		var body containerCreate
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Labels["cloudmeter.product_test_id"] != "test-id" {
			t.Fatalf("labels=%v", body.Labels)
		}
		if body.Labels["cloudmeter.owner"] != "stack-a" {
			t.Fatalf("owner label=%v", body.Labels)
		}
		if _, hasAppID := body.Labels["cloudmeter.app_id"]; hasAppID {
			t.Fatalf("test container unexpectedly has application label: %v", body.Labels)
		}
		restart, ok := body.HostConfig["RestartPolicy"].(map[string]any)
		if !ok || restart["Name"] != "no" {
			t.Fatalf("RestartPolicy=%#v", body.HostConfig["RestartPolicy"])
		}
		if _, hasBinds := body.HostConfig["Binds"]; hasBinds {
			t.Fatalf("test container must not use persistent binds: %#v", body.HostConfig["Binds"])
		}
		tmpfs, ok := body.HostConfig["Tmpfs"].(map[string]any)
		if !ok || tmpfs["/data"] != "rw,nosuid,nodev,size=2147483648" {
			t.Fatalf("Tmpfs=%#v", body.HostConfig["Tmpfs"])
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"Id":"container"}`)), Header: make(http.Header)}, nil
	})}}
	err := executor.CreateProductTest(context.Background(), "cm-test-id", "example@sha256:"+strings.Repeat("a", 64), "test_net", []string{"test-app"}, map[string]any{
		"testId": "test-id", "cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0,
		"volumes": []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 2.0}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
