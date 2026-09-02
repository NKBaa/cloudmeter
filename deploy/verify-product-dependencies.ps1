$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$Compose = @('docker', 'compose')
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @('--project-name', $env:COMPOSE_PROJECT_NAME) }
$Compose += @('--env-file', '.env', '-f', 'deploy/compose.yaml')
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$Port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$Api = "http://127.0.0.1:$Port/api"
$Image = 'nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10'

function Invoke-Db([string]$Query) {
    $value = & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc $Query
    if ($LASTEXITCODE -ne 0) { throw 'database query failed' }
    return (($value | Select-Object -First 1) -as [string]).Trim()
}

function Invoke-DbQuiet([string]$Query) {
    try { Invoke-Db $Query | Out-Null } catch { }
}

function New-Session([string]$UserID) {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $token = ([Convert]::ToBase64String($bytes)).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    $id = Invoke-Db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$UserID',digest('$token','sha256'),now()+interval '30 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}

function Get-Status([string]$Method, [string]$Uri, [hashtable]$Headers = $null, [string]$Body = '') {
    try {
        $params = @{ UseBasicParsing = $true; Method = $Method; Uri = $Uri }
        if ($Headers) { $params.Headers = $Headers }
        if ($Body) { $params.ContentType = 'application/json'; $params.Body = $Body }
        return [int](Invoke-WebRequest @params).StatusCode
    } catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        throw
    }
}

function Wait-Db([string]$Query, [string]$Expected, [int]$Seconds = 120) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $value = ''
    do {
        $value = Invoke-Db $Query
        if ($value -eq $Expected) { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for $Expected; last value was $value"
}

function Wait-Job([string]$JobID, [string]$Expected = 'succeeded', [int]$Seconds = 120) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $state = ''
    do {
        $state = Invoke-Db "SELECT state FROM deployment_jobs WHERE id='$JobID'"
        if ($state -eq $Expected) { return }
        if ($state -eq 'failed' -and $Expected -ne 'failed') {
            $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$JobID'"
            throw "deployment failed: $lastError"
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for deployment $JobID to reach $Expected; last state was $state"
}

function Wait-ProductTest([string]$TestID, [int]$Seconds = 120) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $state = ''
    do {
        $state = Invoke-Db "SELECT state FROM app_product_version_tests WHERE id='$TestID'"
        if ($state -eq 'succeeded') { return }
        if ($state -eq 'failed') {
            $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM app_product_version_tests WHERE id='$TestID'"
            throw "product test failed: $lastError"
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for product test $TestID; last state was $state"
}

function Get-RuntimeScope([string]$Owner) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $hash = $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Owner.Trim())) } finally { $sha.Dispose() }
    return (($hash[0..4] | ForEach-Object { $_.ToString('x2') }) -join '')
}

function Get-UserNetworkName([string]$Owner, [string]$UserID) {
    if ($Owner.Trim().ToLowerInvariant() -eq 'cloudmeter') { return "user_net_$UserID" }
    return "user_net_$(Get-RuntimeScope $Owner)-$UserID"
}

function New-VersionBody([array]$Dependencies = @()) {
    return @{
        imageDigest = $Image
        runtimeSpec = @{
            cpuCores = 0.25; memoryMiB = 128; systemDiskGiB = 1
            env = @{}; secretKeys = @(); volumes = @(); dependencies = $Dependencies
        }
        routeSpec = @{ containerPort = 80; basePath = '/'; websocket = $true; sse = $true }
        healthSpec = @{ path = '/'; intervalSeconds = 5; timeoutSeconds = 3 }
        updateSpec = @{ dataPolicy = 'stateless' }
    } | ConvertTo-Json -Depth 10 -Compress
}

function Publish-Version([string]$ProductID, [string]$VersionID, [hashtable]$Headers) {
    $queued = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$ProductID/versions/$VersionID/tests" -Headers $Headers -ContentType 'application/json' -Body '{"secrets":{}}'
    Wait-ProductTest $queued.testId
    $published = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$ProductID/versions/$VersionID/publish" -Headers $Headers
    if ($published.published -ne $true) { throw 'product version was not published' }
}

$workerID = (& $Compose[0] $Compose[1..($Compose.Length - 1)] ps -q worker).Trim()
if (-not $workerID) { throw 'worker must be running' }
$workerEnv = & docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' $workerID
if ($workerEnv -notcontains 'DOCKER_EXECUTOR_ENABLED=true') { throw 'dependency verification requires DOCKER_EXECUTOR_ENABLED=true' }
$runtimeOwnerLine = $workerEnv | Where-Object { $_ -like 'RUNTIME_OWNER=*' } | Select-Object -First 1
if (-not $runtimeOwnerLine) { throw 'worker must expose RUNTIME_OWNER for scoped verification' }
$RuntimeOwner = $runtimeOwnerLine.Substring('RUNTIME_OWNER='.Length).Trim()
& docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) { throw 'fixed verification image is not available' }

$adminID = Invoke-Db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID) { throw 'an active super administrator is required' }
$adminSession = New-Session $adminID
$adminHeaders = @{ Authorization = "Bearer $($adminSession.Token)" }
$marker = [guid]::NewGuid().ToString('N').Substring(0, 12)
$password = "Dependency-$marker-Password!"
$userID = ''
$userSession = $null
$planID = ''
$baseProductID = ''
$dependentProductID = ''
$baseAppID = ''
$dependentAppID = ''
$workerStopped = $false
$network = ''

function Stop-Worker {
    if (-not $script:workerStopped) {
        & $Compose[0] $Compose[1..($Compose.Length - 1)] stop worker | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'failed to stop worker' }
        $script:workerStopped = $true
    }
}

function Start-Worker {
    if ($script:workerStopped) {
        & $Compose[0] $Compose[1..($Compose.Length - 1)] start worker | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'failed to start worker' }
        $script:workerStopped = $false
    }
}

try {
    $baseProduct = Invoke-RestMethod -Method Post -Uri "$Api/admin/products" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ slug = "verify-foundation-$marker"; name = "Dependency foundation $marker" } | ConvertTo-Json -Compress)
    $baseProductID = $baseProduct.id
    $dependentProduct = Invoke-RestMethod -Method Post -Uri "$Api/admin/products" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ slug = "verify-dependent-$marker"; name = "Dependency consumer $marker" } | ConvertTo-Json -Compress)
    $dependentProductID = $dependentProduct.id

    $unknownID = [guid]::NewGuid().ToString()
    $unknownBody = New-VersionBody @(@{ key = 'unknown'; productId = $unknownID; serviceSlug = 'unknown'; required = $true })
    if ((Get-Status POST "$Api/admin/products/$dependentProductID/versions" $adminHeaders $unknownBody) -ne 400) { throw 'unknown dependency product was accepted' }
    $selfBody = New-VersionBody @(@{ key = 'self'; productId = $baseProductID; serviceSlug = 'foundation'; required = $true })
    if ((Get-Status POST "$Api/admin/products/$baseProductID/versions" $adminHeaders $selfBody) -ne 400) { throw 'self dependency was accepted' }
    $malformed = @{ key = 'bad'; productId = $unknownID; serviceSlug = 'bad'; required = $true; url = 'http://invalid' }
    if ((Get-Status POST "$Api/admin/products/$dependentProductID/versions" $adminHeaders (New-VersionBody @($malformed))) -ne 400) { throw 'dependency with undeclared fields was accepted' }

    $baseVersion = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$baseProductID/versions" -Headers $adminHeaders -ContentType 'application/json' -Body (New-VersionBody)
    Publish-Version $baseProductID $baseVersion.id $adminHeaders

    $dependency = @{ key = 'foundation'; productId = $baseProductID; serviceSlug = 'foundation'; required = $true }
    $dependentVersion = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$dependentProductID/versions" -Headers $adminHeaders -ContentType 'application/json' -Body (New-VersionBody @($dependency))
    Publish-Version $dependentProductID $dependentVersion.id $adminHeaders

    $cycle = @{ key = 'consumer'; productId = $dependentProductID; serviceSlug = 'consumer'; required = $true }
    if ((Get-Status POST "$Api/admin/products/$baseProductID/versions" $adminHeaders (New-VersionBody @($cycle))) -ne 400) { throw 'product dependency cycle was accepted' }

    $plan = Invoke-RestMethod -Method Post -Uri "$Api/admin/plans" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ code = "verify-dependency-$marker"; name = "Dependency verification $marker" } | ConvertTo-Json -Compress)
    $planID = $plan.id
    $planBody = @{
        cyclePriceCents = 0; apps = 4; cpuCores = 2; memoryGiB = 2; dataDiskGiB = 0
        backupStorageGiB = 0; backupOperationsPerMonth = 0; concurrentDeployments = 2
        publicIngresses = 4; ingressOverageEnabled = $false; egressGiB = 1; egressOverageEnabled = $false
        creditGrantCents = 0; allowedProductIds = @($baseProductID, $dependentProductID); effectiveAt = (Get-Date).ToUniversalTime().ToString('o')
    } | ConvertTo-Json -Compress
    $planVersion = Invoke-RestMethod -Method Post -Uri "$Api/admin/plans/$planID/versions" -Headers $adminHeaders -ContentType 'application/json' -Body $planBody

    $user = Invoke-RestMethod -Method Post -Uri "$Api/admin/users" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ email = "dependency-$marker@example.invalid"; password = $password; displayName = 'Dependency verification'; role = 'user' } | ConvertTo-Json -Compress)
    $userID = $user.id
    Invoke-RestMethod -Method Put -Uri "$Api/admin/users/$userID/subscription" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ planVersionId = $planVersion.id; endsAt = $null } | ConvertTo-Json -Compress) | Out-Null
    Invoke-RestMethod -Method Post -Uri "$Api/admin/users/$userID/wallet/adjust" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ amountCents = 1000; businessRef = "dependency-verify/$marker"; note = 'Product dependency verification' } | ConvertTo-Json -Compress) | Out-Null
    $userSession = New-Session $userID
    $userHeaders = @{ Authorization = "Bearer $($userSession.Token)" }
    $network = Get-UserNetworkName $RuntimeOwner $userID

    $catalog = Invoke-RestMethod -Method Get -Uri "$Api/products" -Headers $userHeaders
    $consumerCatalog = $catalog.products | Where-Object { $_.id -eq $dependentProductID -and $_.versionId -eq $dependentVersion.id } | Select-Object -First 1
    if ($null -eq $consumerCatalog -or $consumerCatalog.deployable -ne $false -or $consumerCatalog.missingDependencies -notcontains 'foundation (foundation)') { throw 'catalog did not expose the missing required dependency' }

    $consumerCreate = @{ productId = $dependentProductID; versionId = $dependentVersion.id; slug = 'consumer'; idempotencyKey = "dependency-missing/$marker"; secrets = @{} } | ConvertTo-Json -Compress
    if ((Get-Status POST "$Api/apps" $userHeaders $consumerCreate) -ne 409) { throw 'dependent application was accepted before its required service was running' }

    $baseCreate = @{ productId = $baseProductID; versionId = $baseVersion.id; slug = 'foundation'; idempotencyKey = "dependency-base/$marker"; secrets = @{} } | ConvertTo-Json -Compress
    $baseDeployment = Invoke-RestMethod -Method Post -Uri "$Api/apps" -Headers $userHeaders -ContentType 'application/json' -Body $baseCreate
    $baseAppID = $baseDeployment.appId
    Wait-Job $baseDeployment.jobId
    Wait-Db "SELECT status FROM user_apps WHERE id='$baseAppID'" 'running'

    $readyCatalog = Invoke-RestMethod -Method Get -Uri "$Api/products" -Headers $userHeaders
    $readyConsumer = $readyCatalog.products | Where-Object { $_.id -eq $dependentProductID -and $_.versionId -eq $dependentVersion.id } | Select-Object -First 1
    if ($null -eq $readyConsumer -or $readyConsumer.deployable -ne $true -or @($readyConsumer.missingDependencies).Count -ne 0) { throw 'catalog did not become deployable after the dependency started' }

    $dependentDeployment = Invoke-RestMethod -Method Post -Uri "$Api/apps" -Headers $userHeaders -ContentType 'application/json' -Body $consumerCreate.Replace("dependency-missing/$marker", "dependency-consumer/$marker")
    $dependentAppID = $dependentDeployment.appId
    Wait-Job $dependentDeployment.jobId
    $dependentContainer = Invoke-Db "SELECT upstream_container FROM app_routes WHERE user_app_id='$dependentAppID'"
    $environment = @(& docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' $dependentContainer)
    $upper = ($environment | Where-Object { $_ -like 'NO_PROXY=*' } | Select-Object -First 1) -replace '^NO_PROXY=', ''
    $lower = ($environment | Where-Object { $_ -like 'no_proxy=*' } | Select-Object -First 1) -replace '^no_proxy=', ''
    if (($upper -split ',') -notcontains 'foundation' -or ($lower -split ',') -notcontains 'foundation') { throw 'dependency service alias is missing from NO_PROXY/no_proxy' }
    $body = & docker exec $dependentContainer wget -Y off -q -T 5 -O - http://foundation/
    if ($LASTEXITCODE -ne 0 -or ($body -join "`n") -notlike '*Welcome to nginx*') { throw 'same-account stable dependency alias is not reachable' }

    & (Join-Path $PSScriptRoot 'verify-runtime-isolation.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'runtime isolation verification failed' }

    Stop-Worker
    $queuedUpdate = Invoke-RestMethod -Method Post -Uri "$Api/apps/$dependentAppID/releases" -Headers $userHeaders -ContentType 'application/json' -Body (@{ versionId = $dependentVersion.id; idempotencyKey = "dependency-race/$marker" } | ConvertTo-Json -Compress)
    $baseContainer = Invoke-Db "SELECT upstream_container FROM app_routes WHERE user_app_id='$baseAppID'"
    & docker rm -f $baseContainer | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'failed to invalidate the dependency container' }
    Start-Worker
    Wait-Job $queuedUpdate.jobId 'failed'
    $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$($queuedUpdate.jobId)'"
    if ($lastError -notlike '*required dependencies are unavailable*') { throw "worker did not record the dependency failure: $lastError" }
    if ((Invoke-Db "SELECT status FROM user_apps WHERE id='$dependentAppID'") -ne 'running') { throw 'failed dependency recheck did not preserve the previous active release' }

    $baseRecovery = Invoke-RestMethod -Method Post -Uri "$Api/apps/$baseAppID/releases" -Headers $userHeaders -ContentType 'application/json' -Body (@{ versionId = $baseVersion.id; idempotencyKey = "dependency-recovery/$marker" } | ConvertTo-Json -Compress)
    Wait-Job $baseRecovery.jobId
    $recoveredContainer = Invoke-Db "SELECT upstream_container FROM app_routes WHERE user_app_id='$baseAppID'"
    if ($recoveredContainer -eq $baseContainer) { throw 'dependency recovery did not create a new immutable release container' }
    $body = & docker exec $dependentContainer wget -Y off -q -T 5 -O - http://foundation/
    if ($LASTEXITCODE -ne 0 -or ($body -join "`n") -notlike '*Welcome to nginx*') { throw 'stable dependency alias did not follow the recovered release' }

    Write-Host 'Product dependency validation, catalog readiness, deployment gate, queued recheck, stable alias and NO_PROXY verification passed'
} finally {
    try { Start-Worker } catch { }
    if ($userSession) { Invoke-DbQuiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($userSession.ID)'" }
    Invoke-DbQuiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($adminSession.ID)'"
    if ($userID) {
        Invoke-DbQuiet "UPDATE deployment_jobs SET state='failed',last_error=coalesce(last_error,'dependency verification cleanup'),updated_at=now() WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id='$userID') AND state NOT IN ('succeeded','failed'); DELETE FROM app_routes WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id='$userID'); UPDATE user_apps SET status='suspended',suspension_reason=NULL WHERE user_id='$userID'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$userID'"
        foreach ($verifiedAppID in @($baseAppID, $dependentAppID)) {
            if (-not $verifiedAppID) { continue }
            $containers = @(& docker ps -a --filter "label=cloudmeter.owner=$RuntimeOwner" --filter "label=cloudmeter.app_id=$verifiedAppID" --format '{{.Names}}')
            foreach ($container in $containers) { try { & docker rm -f $container *> $null } catch { } }
        }
        if ($network) {
            $routerID = (& $Compose[0] $Compose[1..($Compose.Length - 1)] ps -q app-router).Trim()
            $proxyID = (& $Compose[0] $Compose[1..($Compose.Length - 1)] ps -q egress-proxy).Trim()
            foreach ($containerID in @($routerID, $proxyID)) { if ($containerID) { try { & docker network disconnect -f $network $containerID *> $null } catch { } } }
            try { & docker network rm $network *> $null } catch { }
        }
    }
    if ($planID) { Invoke-DbQuiet "UPDATE plans SET purchase_enabled=false WHERE id='$planID'" }
    if ($baseProductID -or $dependentProductID) {
        Invoke-DbQuiet "UPDATE app_products SET status='retired' WHERE id IN (nullif('$baseProductID','')::uuid,nullif('$dependentProductID','')::uuid)"
    }
}
