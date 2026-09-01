$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
$Compose = @("docker","compose")
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @("--project-name", $env:COMPOSE_PROJECT_NAME) }
$Compose += @("--env-file",".env","-f","deploy/compose.yaml")
if (-not (Test-Path ".env")) { throw ".env is required" }
$latestMigration = Get-ChildItem "migrations/*.up.sql" |
    ForEach-Object { if ($_.BaseName -match '^(\d+)_') { [int]$Matches[1] } } |
    Measure-Object -Maximum |
    Select-Object -ExpandProperty Maximum
if (-not $latestMigration) { throw "no migrations found" }
& $Compose[0] $Compose[1..($Compose.Length-1)] config --quiet
if ($LASTEXITCODE -ne 0) { throw "compose config failed" }
& $Compose[0] $Compose[1..($Compose.Length-1)] ps
$workerID = (& $Compose[0] $Compose[1..($Compose.Length-1)] ps -q worker).Trim()
$apiID = (& $Compose[0] $Compose[1..($Compose.Length-1)] ps -q api).Trim()
$egressID = (& $Compose[0] $Compose[1..($Compose.Length-1)] ps -q egress-proxy).Trim()
if (-not $workerID -or -not $apiID -or -not $egressID) { throw "api, worker and egress-proxy containers must be running" }
$expectedProject = if ($env:COMPOSE_PROJECT_NAME) { $env:COMPOSE_PROJECT_NAME } else { "cloudmeter" }
foreach ($entry in @{ Worker = $workerID; API = $apiID; EgressProxy = $egressID }.GetEnumerator()) {
    $labels = ((& docker inspect --format '{{json .Config.Labels}}' $entry.Value).Trim() | ConvertFrom-Json)
    $actualProject = [string]$labels.'com.docker.compose.project'
    if ($actualProject -ne $expectedProject) { throw "$($entry.Key) belongs to Compose project $actualProject, expected $expectedProject" }
}
$egressHealth = (& docker inspect --format '{{.State.Health.Status}}' $egressID).Trim()
if ($egressHealth -ne "healthy") { throw "egress-proxy must be healthy" }
$workerEnv = & docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' $workerID
$workerMounts = & docker inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' $workerID
$apiMounts = & docker inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' $apiID
if ($workerEnv -contains "DOCKER_EXECUTOR_ENABLED=true" -and $workerMounts -notcontains "/var/run/docker.sock") {
    throw "Docker executor is enabled but worker has no Docker socket"
}
if ($apiMounts -contains "/var/run/docker.sock") { throw "API must never mount the Docker socket" }
$port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$envLine = Get-Content .env | Where-Object { $_ -match "^PLATFORM_PORT=" } | Select-Object -Last 1
if (-not $env:PLATFORM_PORT -and $envLine) { $port = [int](($envLine -split "=",2)[1]) }
$bindIP = if ($env:PLATFORM_BIND_IP) { $env:PLATFORM_BIND_IP } else { "127.0.0.1" }
$gatewayIP = if ($env:GATEWAY_BIND_IP) { $env:GATEWAY_BIND_IP } else { "127.0.0.1" }
$health = Invoke-RestMethod "http://${bindIP}:$port/api/healthz"
if ($health.status -ne "ok") { throw "health check failed" }
& $Compose[0] $Compose[1..($Compose.Length-1)] run --rm migrate
if ($LASTEXITCODE -ne 0) { throw "migration check failed" }
$migrationState = (& $Compose[0] $Compose[1..($Compose.Length-1)] exec -T postgres psql -U cloudmeter -d cloudmeter -Atc "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations").Trim()
if ($LASTEXITCODE -ne 0) { throw "migration state query failed" }
if ($migrationState -ne "$latestMigration|clean") { throw "migration state is $migrationState, expected $latestMigration|clean" }
& $Compose[0] $Compose[1..($Compose.Length-1)] exec -T gateway caddy validate --config /etc/cloudmeter/runtime/Caddyfile --adapter caddyfile
if ($LASTEXITCODE -ne 0) { throw "Caddy gateway configuration validation failed" }
docker run --rm -v "${Root}:/src" -w /src golang:1.23-alpine sh -c "/usr/local/go/bin/go test ./internal/httpapi -run TestOpenAPICoversRegisteredRoutes -count=1"
if ($LASTEXITCODE -ne 0) { throw "OpenAPI route contract check failed" }
docker run --rm -v "${Root}:/work" -w /work node:24-alpine npx --yes '@redocly/cli@2.46.1' lint docs/openapi.yaml --config docs/redocly.yaml
if ($LASTEXITCODE -ne 0) { throw "OpenAPI schema validation failed" }
& $Compose[0] $Compose[1..($Compose.Length-1)] restart api worker egress-proxy app-router web gateway
if ($LASTEXITCODE -ne 0) { throw "service restart failed" }
foreach ($service in @("api", "app-router", "egress-proxy", "web", "gateway")) {
    $containerID = (& $Compose[0] $Compose[1..($Compose.Length-1)] ps -q $service).Trim()
    if (-not $containerID) { throw "$service container is not running" }
    $serviceHealth = ""
    foreach ($attempt in 1..30) {
        $serviceHealth = (& docker inspect --format '{{.State.Health.Status}}' $containerID).Trim()
        if ($serviceHealth -eq "healthy") { break }
        if ($serviceHealth -eq "unhealthy") { throw "$service is unhealthy" }
        Start-Sleep -Seconds 2
    }
    if ($serviceHealth -ne "healthy") { throw "timed out waiting for $service health" }
}
$health = Invoke-RestMethod "http://${bindIP}:$port/api/healthz"
if ($health.status -ne "ok") { throw "post-restart health check failed" }
$unknownHostStatus = 0
try {
    $unknownResponse = Invoke-WebRequest "http://${gatewayIP}:80/api/healthz" -Headers @{ Host = "invalid-host.example.invalid" } -UseBasicParsing
    $unknownHostStatus = [int]$unknownResponse.StatusCode
} catch {
    if (-not $_.Exception.Response) { throw }
    $unknownHostStatus = [int]$_.Exception.Response.StatusCode
}
if ($unknownHostStatus -ne 421) { throw "unknown Host must return 421, got $unknownHostStatus" }
& $Compose[0] $Compose[1..($Compose.Length-1)] ps
