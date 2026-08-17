$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
$Compose = @("docker", "compose")
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @("--project-name", $env:COMPOSE_PROJECT_NAME) }
$Compose += @("--env-file", ".env", "-f", "deploy/compose.yaml")
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
function Get-ContainerID([string]$service) { return ((& $Compose[0] $Compose[1..($Compose.Length-1)] ps -q $service) -as [string]).Trim() }
function Inspect-Value([string]$id, [string]$format) { return ((& docker inspect --format $format $id) -as [string]).Trim() }
function Inspect-Lines([string]$id, [string]$format) { return @(& docker inspect --format $format $id | Where-Object { $_ -ne "" }) }
function Get-ContainerOwner([string]$id) {
    return ((Inspect-Value $id '{{json .Config.Labels}}') | ConvertFrom-Json).'cloudmeter.owner'
}
function Get-NetworkAliases([string]$id, [string]$network) {
    $networks = (Inspect-Value $id '{{json .NetworkSettings.Networks}}') | ConvertFrom-Json
    $endpoint = $networks.PSObject.Properties[$network]
    if (-not $endpoint) { return @() }
    return @($endpoint.Value.Aliases)
}
function Get-RuntimeScope([string]$Owner) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $hash = $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Owner.Trim())) } finally { $sha.Dispose() }
    return (($hash[0..4] | ForEach-Object { $_.ToString('x2') }) -join '')
}
$workerID = Get-ContainerID "worker"
$routerID = Get-ContainerID "app-router"
$proxyID = Get-ContainerID "egress-proxy"
if (-not $workerID -or -not $routerID -or -not $proxyID) { throw "worker, app-router and egress-proxy must be running" }
$workerEnv = @(& docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' $workerID)
$runtimeOwnerLine = $workerEnv | Where-Object { $_ -like 'RUNTIME_OWNER=*' } | Select-Object -First 1
if (-not $runtimeOwnerLine) { throw 'worker must expose RUNTIME_OWNER for scoped verification' }
$RuntimeOwner = $runtimeOwnerLine.Substring('RUNTIME_OWNER='.Length).Trim()
if ((Get-ContainerOwner $routerID) -ne $RuntimeOwner) { throw 'app-router owner label does not match worker runtime owner' }
if ((Get-ContainerOwner $proxyID) -ne $RuntimeOwner) { throw 'egress-proxy owner label does not match worker runtime owner' }
$appIDs = @(& docker ps --filter "label=cloudmeter.owner=$RuntimeOwner" --filter "name=cm-" -q)
if (-not $appIDs.Count) { throw "at least one running CloudMeter application container is required" }
$apps = @()
foreach ($id in $appIDs) {
    $name = (Inspect-Value $id '{{.Name}}').TrimStart('/')
    if ((Inspect-Value $id '{{.HostConfig.Privileged}}') -ne 'false') { throw "user container $name is privileged" }
    if ((Inspect-Value $id '{{.HostConfig.NetworkMode}}') -eq 'host') { throw "user container $name uses host networking" }
    if ((Inspect-Value $id '{{.HostConfig.PidMode}}') -eq 'host') { throw "user container $name uses the host PID namespace" }
    if ((Inspect-Lines $id '{{range .HostConfig.SecurityOpt}}{{println .}}{{end}}') -notcontains 'no-new-privileges:true') { throw "user container $name is missing no-new-privileges" }
    foreach ($bind in (Inspect-Lines $id '{{range .HostConfig.Binds}}{{println .}}{{end}}')) {
        if ($bind -match '(^|:)/var/run/docker.sock(:|$)') { throw "user container $name mounts the Docker socket" }
        $source = ($bind -split ':', 2)[0]
        if ($source.StartsWith('/') -or $source -match '^[A-Za-z]:[\/]') { throw "user container $name has a host path bind" }
    }
    [string[]]$networkNames = @(Inspect-Lines $id '{{range $name,$endpoint := .NetworkSettings.Networks}}{{println $name}}{{end}}')
    if ($networkNames.Count -ne 1 -or -not $networkNames[0].StartsWith('user_net_')) { throw "user container $name must only join its user network" }
    $network = $networkNames[0]
    if ((& docker network inspect --format '{{.Internal}}' $network).Trim() -ne 'true') { throw "user network $network is not internal" }
    $releaseAlias = (Get-NetworkAliases $id $network) | Where-Object { $_ -like 'release-*' } | Select-Object -First 1
    if (-not $releaseAlias) { throw "user container $name has no immutable release alias" }
    $apps += [pscustomobject]@{ Network = $network; Alias = $releaseAlias; Image = (Inspect-Value $id '{{.Config.Image}}') }
}
$activeNetworks = @($apps.Network | Sort-Object -Unique)
foreach ($network in $activeNetworks) {
    if ((Get-NetworkAliases $routerID $network) -notcontains 'cloudmeter-app-router') { throw "router is not attached to $network with its stable alias" }
    if ((Get-NetworkAliases $proxyID $network) -notcontains 'cloudmeter-egress-proxy') { throw "egress proxy is not attached to $network with its stable alias" }
}
$same = $apps[0]
& docker run --rm --network $same.Network --cap-drop ALL --security-opt no-new-privileges:true $same.Image wget -q -T 5 -O /dev/null "http://$($same.Alias)"
if ($LASTEXITCODE -ne 0) { throw "same-user stable release alias is not reachable" }
foreach ($platformName in @("postgres", "api", "redis")) {
    & docker run --rm --network $same.Network --cap-drop ALL --security-opt no-new-privileges:true $same.Image getent hosts $platformName 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { throw "user network can resolve platform service $platformName" }
}
$temporaryNetwork = "user_net_verify_$(Get-RuntimeScope $RuntimeOwner)_$([guid]::NewGuid().ToString('N'))"
try {
    & docker network create --internal --label "cloudmeter.managed=true" --label "cloudmeter.owner=$RuntimeOwner" $temporaryNetwork | Out-Null
    & docker run --rm --network $temporaryNetwork --cap-drop ALL --security-opt no-new-privileges:true $same.Image getent hosts $same.Alias 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { throw "cross-user network resolved another user's release alias" }
} finally { & docker network rm $temporaryNetwork 2>$null | Out-Null }
if ((Inspect-Lines $workerID '{{range .Mounts}}{{println .Destination}}{{end}}') -notcontains '/var/run/docker.sock') { throw "worker does not have the Docker socket" }
Write-Host "Runtime isolation smoke test passed for $($apps.Count) application container(s) across $($activeNetworks.Count) user network(s)"
