$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { throw "Docker is required." }
& docker compose version | Out-Null
if ($LASTEXITCODE -ne 0) { throw "Docker Compose v2 is required." }

function New-RandomBytes([int]$Length) {
    $bytes = New-Object byte[] $Length
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($bytes) } finally { $generator.Dispose() }
    return $bytes
}

function New-HexSecret {
    return -join ((New-RandomBytes 32) | ForEach-Object { $_.ToString('x2') })
}

function New-Base64Secret {
    return [Convert]::ToBase64String((New-RandomBytes 32)).TrimEnd('=')
}

if (-not (Test-Path .env)) {
    $port = if ($env:PLATFORM_PORT) { $env:PLATFORM_PORT } else { "8080" }
    $content = @(
        "PLATFORM_BIND_IP=0.0.0.0"
        "PLATFORM_PORT=$port"
        "GATEWAY_ACCESS_MODE="
        "GATEWAY_BIND_IP=0.0.0.0"
        "GATEWAY_TRUSTED_PROXY_CIDRS=172.16.0.0/12"
        "CADDY_ADMIN_URL=http://gateway:2019"
        "CADDYFILE_PATH=/etc/cloudmeter/runtime/Caddyfile"
        "API_TRUSTED_PROXY_CIDRS=172.16.0.0/12"
        "POSTGRES_PASSWORD=$(New-HexSecret)"
        "REDIS_PASSWORD=$(New-HexSecret)"
        "SESSION_TTL_HOURS=24"
        "SECRETS_ENCRYPTION_KEY=$(New-Base64Secret)"
        "DOCKER_EXECUTOR_ENABLED=true"
        "DOCKER_SOCKET=/var/run/docker.sock"
        "DOCKER_SOCKET_PATH=/var/run/docker.sock"
        "ROUTER_INTERNAL_TOKEN=$(New-HexSecret)"
        "EGRESS_INGEST_TOKEN=$(New-HexSecret)"
        "BACKUP_STORAGE_VOLUME=cloudmeter_backup_data"
        "BACKUP_STORAGE_MOUNT_VOLUME=cloudmeter_backup_data"
        "BACKUP_HELPER_IMAGE=nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10"
    )
    [IO.File]::WriteAllLines((Join-Path $Root '.env'), $content, [Text.UTF8Encoding]::new($false))
    Write-Host "Generated .env with random credentials. Back up this file securely."
} else {
    Write-Host "Using existing .env; stored credentials were not changed."
}

& docker compose --env-file .env -f deploy/compose.yaml config --quiet
if ($LASTEXITCODE -ne 0) { throw "Compose configuration is invalid." }
& docker compose --env-file .env -f deploy/compose.yaml up -d --build
if ($LASTEXITCODE -ne 0) { throw "CloudMeter deployment failed." }

$portLine = Get-Content .env | Where-Object { $_ -match '^PLATFORM_PORT=' } | Select-Object -Last 1
$port = if ($portLine) { ($portLine -split '=', 2)[1] } else { "8080" }
Write-Host ""
Write-Host "CloudMeter is starting. Open: http://127.0.0.1:$port/setup"
Write-Host "For a remote server, replace 127.0.0.1 with its IP address."
Write-Host "After setup, configure the server public URL in Infrastructure > Website Settings."
