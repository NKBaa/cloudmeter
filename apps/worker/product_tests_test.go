package main

import (
	"cloudmeter/internal/secretbox"
	"errors"
	"strings"
	"testing"
)

func TestProductVersionTestRuntimeInjectsSecretsWithoutMutatingSnapshot(t *testing.T) {
	previousSecrets, previousProxy, previousToken := secrets, egressProxyContainer, egressToken
	t.Cleanup(func() {
		secrets, egressProxyContainer, egressToken = previousSecrets, previousProxy, previousToken
	})

	box, err := secretbox.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	secrets = box
	egressProxyContainer = ""
	egressToken = ""
	encrypted, err := box.Encrypt("product.version.test.test-id.API_KEY", "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	runtimeSpec := map[string]any{
		"cpuCores":      1.0,
		"memoryMiB":     512.0,
		"systemDiskGiB": 5.0,
		"env":           map[string]any{"APP_MODE": "test"},
	}
	runtime, err := productVersionTestRuntime("test-id", runtimeSpec, map[string]string{"API_KEY": encrypted})
	if err != nil {
		t.Fatal(err)
	}
	env, ok := runtime["env"].(map[string]any)
	if !ok || env["APP_MODE"] != "test" || env["API_KEY"] != "secret-value" {
		t.Fatalf("runtime env=%#v", runtime["env"])
	}
	originalEnv := runtimeSpec["env"].(map[string]any)
	if _, found := originalEnv["API_KEY"]; found {
		t.Fatalf("runtime snapshot env was mutated: %#v", originalEnv)
	}
	if runtime["testId"] != "test-id" {
		t.Fatalf("test ID=%#v", runtime["testId"])
	}
}

func TestProductVersionTestRuntimeAddsAuthenticatedProxy(t *testing.T) {
	previousSecrets, previousProxy, previousToken := secrets, egressProxyContainer, egressToken
	t.Cleanup(func() {
		secrets, egressProxyContainer, egressToken = previousSecrets, previousProxy, previousToken
	})
	secrets = nil
	egressProxyContainer = "cloudmeter-egress-proxy-1"
	egressToken = "test-token"
	runtime, err := productVersionTestRuntime("11111111-2222-3333-4444-555555555555", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := runtime["env"].(map[string]any)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		value, ok := env[key].(string)
		if !ok || !strings.Contains(value, "@cloudmeter-egress-proxy:3128") {
			t.Fatalf("%s=%#v", key, env[key])
		}
	}
	if env["NO_PROXY"] != "localhost,127.0.0.1,::1" {
		t.Fatalf("NO_PROXY=%#v", env["NO_PROXY"])
	}
}

func TestProductTestContainerNameParsing(t *testing.T) {
	id := "11111111-2222-3333-4444-555555555555"
	if got := productTestContainerName(id); got != "cm-test-"+id {
		t.Fatalf("container=%s", got)
	}
	if got := productTestNetworkName(id); got != "cm-test-net-11111111222233334444555555555555" {
		t.Fatalf("network=%s", got)
	}
	if got, ok := productTestIDFromContainerName("cm-test-" + id); !ok || got != id {
		t.Fatalf("parsed=%q ok=%v", got, ok)
	}
	for _, name := range []string{"cm-test-health-111111111111", "cm-test-not-a-uuid", "cm-" + id} {
		if _, ok := productTestIDFromContainerName(name); ok {
			t.Fatalf("unexpected product test container: %s", name)
		}
	}
	if got := productTestHealthContainerName(id); got != "cm-test-health-"+id {
		t.Fatalf("health probe name=%s", got)
	}
	if got := productTestHealthLegacyContainerName(id); got != "cm-test-health-111111112222" {
		t.Fatalf("legacy health probe name=%s", got)
	}
	for _, name := range []string{productTestHealthContainerName(id), productTestHealthLegacyContainerName(id)} {
		if !isProductTestHealthContainerName(name) {
			t.Fatalf("health probe name not recognized: %s", name)
		}
	}
	if isProductTestHealthContainerName("cm-test-health-invalid") {
		t.Fatal("invalid health probe name recognized")
	}
	network := "cm-test-net-11111111222233334444555555555555"
	if got, ok := productTestIDFromNetworkName(network); !ok || got != id {
		t.Fatalf("network parsed=%q ok=%v", got, ok)
	}
	if _, ok := productTestIDFromNetworkName("cm-test-net-invalid"); ok {
		t.Fatal("invalid test network parsed")
	}
	active := map[string]struct{}{id: {}}
	for _, name := range []string{productTestHealthContainerName(id), productTestHealthLegacyContainerName(id)} {
		if !productTestHealthOwnerActive(name, active) {
			t.Fatalf("active health probe owner not recognized: %s", name)
		}
	}
	if productTestHealthOwnerActive("cm-test-health-aaaaaaaaaaaa", active) {
		t.Fatal("unrelated health probe matched active test")
	}
}

func TestScopedProductTestNames(t *testing.T) {
	previous := runtimeScope
	runtimeScope = runtimeScopeToken("cloudmeter-test")
	t.Cleanup(func() { runtimeScope = previous })
	id := "11111111-2222-3333-4444-555555555555"
	if got, ok := productTestIDFromContainerName(productTestContainerName(id)); !ok || got != id {
		t.Fatalf("scoped container parsed=%q ok=%v", got, ok)
	}
	if got, ok := productTestIDFromNetworkName(productTestNetworkName(id)); !ok || got != id {
		t.Fatalf("scoped network parsed=%q ok=%v", got, ok)
	}
	if !isProductTestHealthContainerName(productTestHealthContainerName(id)) {
		t.Fatal("scoped health probe name was not recognized")
	}
	if productTestContainerName(id) == legacyProductTestContainerName(id) {
		t.Fatal("scoped product test name did not change")
	}
}

func TestProductTestFailureIsActionableAndRedacted(t *testing.T) {
	failure := productTestFailure(
		"拉取应用镜像",
		"registry.example.com/team/app:v1",
		errors.New(`docker POST https://user:pass@registry.example.com: unauthorized token=top-secret password=also-secret`),
		[]string{"top-secret"},
	).Error()
	for _, expected := range []string{"测试阶段：拉取应用镜像", "Registry 拒绝访问镜像", "处理建议：", "技术详情（已脱敏）："} {
		if !strings.Contains(failure, expected) {
			t.Fatalf("failure missing %q: %s", expected, failure)
		}
	}
	for _, secret := range []string{"user:pass", "top-secret", "also-secret"} {
		if strings.Contains(failure, secret) {
			t.Fatalf("failure leaked %q: %s", secret, failure)
		}
	}
}
