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
$LatestMigration = (Get-ChildItem (Join-Path $Root 'migrations/*.up.sql') | ForEach-Object { if ($_.Name -match '^0*(\d+)_') { [int]$Matches[1] } } | Sort-Object | Select-Object -Last 1)

function Invoke-Db([string]$Query) {
    $value = & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc $Query
    if ($LASTEXITCODE -ne 0) { throw 'database query failed' }
    return (($value | Select-Object -First 1) -as [string]).Trim()
}

function Invoke-DbQuiet([string]$Query) {
    try { Invoke-Db $Query | Out-Null } catch { }
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

function Assert-DbRejected([string]$Query, [string]$Expected) {
    $oldPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $output = (& $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -c $Query 2>&1 | Out-String)
    $code = $LASTEXITCODE
    $ErrorActionPreference = $oldPreference
    if ($code -eq 0 -or $output -notlike "*$Expected*") {
        throw "database mutation was not rejected as expected: $output"
    }
}

function Wait-Db([string]$Query, [string]$Expected, [int]$Seconds = 90) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $value = ''
    do {
        $value = Invoke-Db $Query
        if ($value -eq $Expected) { return }
        if ($value -eq 'failed' -and $Expected -ne 'failed') {
            $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM app_product_version_tests WHERE id='$script:testID'"
            throw "product test failed: $lastError"
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for $Expected; last value was $value"
}

function Wait-RuntimeAbsent([string]$Container, [string]$Network, [int]$Seconds = 30) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        $containerExists = $true
        $networkExists = $true
        try { & docker container inspect $Container *> $null; $containerExists = ($LASTEXITCODE -eq 0) } catch { $containerExists = $false }
        try { & docker network inspect $Network *> $null; $networkExists = ($LASTEXITCODE -eq 0) } catch { $networkExists = $false }
        if (-not $containerExists -and -not $networkExists) { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "runtime cleanup timed out for $Container and $Network"
}

function Invoke-DockerCleanup([string[]]$Arguments) {
    $oldPreference = $ErrorActionPreference
    $ErrorActionPreference = 'SilentlyContinue'
    try { & docker @Arguments *> $null } catch { }
    finally { $ErrorActionPreference = $oldPreference }
}

function Test-DockerObjectExists([string[]]$Arguments) {
    $oldPreference = $ErrorActionPreference
    $ErrorActionPreference = 'SilentlyContinue'
    try {
        & docker @Arguments *> $null
        return ($LASTEXITCODE -eq 0)
    } catch {
        return $false
    } finally {
        $ErrorActionPreference = $oldPreference
    }
}

function New-SessionToken {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return ([Convert]::ToBase64String($bytes)).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function Get-RuntimeScope([string]$Owner) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $hash = $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Owner.Trim())) } finally { $sha.Dispose() }
    return (($hash[0..4] | ForEach-Object { $_.ToString('x2') }) -join '')
}

$workerID = (& $Compose[0] $Compose[1..($Compose.Length - 1)] ps -q worker).Trim()
if (-not $workerID) { throw 'worker must be running' }
$workerEnv = & docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' $workerID
if ($workerEnv -notcontains 'DOCKER_EXECUTOR_ENABLED=true') { throw 'product-version runtime verification requires DOCKER_EXECUTOR_ENABLED=true' }
$runtimeOwnerLine = $workerEnv | Where-Object { $_ -like 'RUNTIME_OWNER=*' } | Select-Object -First 1
if (-not $runtimeOwnerLine) { throw 'worker must expose RUNTIME_OWNER for scoped verification' }
$RuntimeOwner = $runtimeOwnerLine.Substring('RUNTIME_OWNER='.Length).Trim()
$RuntimeScope = Get-RuntimeScope $RuntimeOwner
& docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) { throw 'fixed verification image is not available' }
if ((Invoke-Db "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations") -ne "$LatestMigration|clean") { throw "migration $LatestMigration must be applied before verification" }

$adminID = Invoke-Db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID) { throw 'an active super administrator is required' }
$adminToken = New-SessionToken
$adminSessionID = Invoke-Db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$adminID',digest('$adminToken','sha256'),now()+interval '20 minutes') RETURNING id"
$adminHeaders = @{ Authorization = "Bearer $adminToken" }

$marker = [guid]::NewGuid().ToString('N').Substring(0, 12)
$secretValue = "product-test-secret-$marker"
$script:productID = ''
$script:versionID = ''
$script:testID = ''
$script:testContainer = ''
$script:testNetwork = ''
$script:orphanContainer = ''
$script:orphanProbe = ''
$script:orphanNetwork = ''
$script:workerStopped = $false

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
    if ((Get-Status POST "$Api/admin/products" $adminHeaders '{"slug":"INVALID_slug","name":"Invalid product"}') -ne 400) { throw 'invalid product slug was accepted' }
    $product = Invoke-RestMethod -Method Post -Uri "$Api/admin/products" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ slug = "verify-product-$marker"; name = "Product test verification $marker" } | ConvertTo-Json -Compress)
    $script:productID = $product.id
    if ($script:productID -notmatch '^[0-9a-f-]{36}$') { throw 'product creation returned an invalid id' }
    if ((Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='product.create' AND resource_id='$($script:productID)'") -ne '1') { throw 'product creation audit count is incorrect' }

    $updatedName = "Product lifecycle verification $marker"
    $updated = Invoke-RestMethod -Method Patch -Uri "$Api/admin/products/$($script:productID)" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ name = $updatedName } | ConvertTo-Json -Compress)
    $updatedReplay = Invoke-RestMethod -Method Patch -Uri "$Api/admin/products/$($script:productID)" -Headers $adminHeaders -ContentType 'application/json' -Body (@{ name = $updatedName } | ConvertTo-Json -Compress)
    if ($updated.name -ne $updatedName -or $updated.idempotent -ne $false -or $updatedReplay.idempotent -ne $true) { throw 'product name update was not idempotent' }
    if ((Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='product.update' AND resource_id='$($script:productID)'") -ne '1') { throw 'product update audit count is incorrect' }
    Assert-DbRejected "UPDATE app_products SET id=gen_random_uuid() WHERE id='$($script:productID)'" 'product identity is immutable'
    Assert-DbRejected "UPDATE app_products SET slug='changed-$marker' WHERE id='$($script:productID)'" 'product identity is immutable'
    Assert-DbRejected "UPDATE app_products SET created_at=created_at+interval '1 second' WHERE id='$($script:productID)'" 'product identity is immutable'
    Assert-DbRejected "DELETE FROM app_products WHERE id='$($script:productID)'" 'immutable history cannot be deleted'
    Assert-DbRejected 'BEGIN; TRUNCATE app_products CASCADE; ROLLBACK;' 'immutable history cannot be truncated'

    $versionBody = @{
        imageDigest = $Image
        runtimeSpec = @{
            cpuCores = 0.25; memoryMiB = 128; systemDiskGiB = 1
            env = @{ VERIFY_MODE = 'acceptance' }
            secretKeys = @('VERIFY_SECRET')
            volumes = @(@{ name = 'data'; mountPath = '/data'; sizeGiB = 1 })
        }
        routeSpec = @{ containerPort = 80; basePath = '/'; websocket = $true; sse = $true }
        healthSpec = @{ path = '/'; intervalSeconds = 10; timeoutSeconds = 3 }
        updateSpec = @{ dataPolicy = 'volume_compatible' }
    } | ConvertTo-Json -Depth 8 -Compress
    $version = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$($script:productID)/versions" -Headers $adminHeaders -ContentType 'application/json' -Body $versionBody
    $script:versionID = $version.id
    if ($script:versionID -notmatch '^[0-9a-f-]{36}$') { throw 'version creation returned an invalid id' }

    if ((Get-Status POST "$Api/admin/products/$($script:productID)/versions/$($script:versionID)/publish" $adminHeaders) -ne 409) { throw 'untested version was published through the API' }
    Assert-DbRejected "UPDATE app_product_versions SET published_at=now() WHERE id='$($script:versionID)'" 'successful product version test is required'
    if ((Get-Status POST "$Api/admin/products/$($script:productID)/versions/$($script:versionID)/tests" $adminHeaders '{"secrets":{}}') -ne 400) { throw 'missing required test secret was accepted' }

    Stop-Worker
    $testBody = @{ secrets = @{ VERIFY_SECRET = $secretValue } } | ConvertTo-Json -Depth 4 -Compress
    $queued = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$($script:productID)/versions/$($script:versionID)/tests" -Headers $adminHeaders -ContentType 'application/json' -Body $testBody
    $script:testID = $queued.testId
    if ($script:testID -notmatch '^[0-9a-f-]{36}$' -or $queued.state -ne 'queued') { throw 'test deployment was not queued' }
    if ((Get-Status PATCH "$Api/admin/products/$($script:productID)/availability" $adminHeaders '{"enabled":false}') -ne 409) { throw 'product was retired while a version test was active' }
    $script:testContainer = "cm-test-$RuntimeScope-$($script:testID)"
    $script:testNetwork = "cm-test-net-$RuntimeScope-$($script:testID.Replace('-', ''))"

    $encrypted = Invoke-Db "SELECT encrypted_secrets->>'VERIFY_SECRET' FROM app_product_version_tests WHERE id='$($script:testID)'"
    if (-not $encrypted.StartsWith('cmsec:v1:') -or $encrypted.Contains($secretValue)) { throw 'test secret was not stored as authenticated ciphertext' }
    Assert-DbRejected "UPDATE app_product_version_tests SET id=gen_random_uuid() WHERE id='$($script:testID)'" 'product version test snapshot is immutable'

    Start-Worker
    Wait-Db "SELECT state FROM app_product_version_tests WHERE id='$($script:testID)'" 'health_checking' 120
    Stop-Worker
    $testInspect = (docker inspect $script:testContainer | ConvertFrom-Json)[0]
    if ($testInspect.HostConfig.RestartPolicy.Name -ne 'no' -or $testInspect.HostConfig.Privileged -ne $false) { throw 'test container runtime policy is unsafe' }
    if (@($testInspect.NetworkSettings.Networks.PSObject.Properties).Count -ne 1) { throw 'test container joined an unexpected number of networks' }
    if (@($testInspect.HostConfig.SecurityOpt) -notcontains 'no-new-privileges:true') { throw 'test container is missing no-new-privileges' }
    if (@($testInspect.Config.Env) -notcontains "VERIFY_SECRET=$secretValue") { throw 'test secret was not passed to the test container' }
    if (@($testInspect.HostConfig.Tmpfs.PSObject.Properties.Name) -notcontains '/data') { throw 'declared data volume was not converted to tmpfs' }
    if ((docker network inspect --format '{{.Internal}}' $script:testNetwork).Trim() -ne 'true') { throw 'test network is not internal' }

    Start-Worker
    Wait-Db "SELECT state FROM app_product_version_tests WHERE id='$($script:testID)'" 'succeeded' 120
    if ((Invoke-Db "SELECT encrypted_secrets::text FROM app_product_version_tests WHERE id='$($script:testID)'") -ne '{}') { throw 'completed test secrets were not cleared' }
    if ((Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='product.test.request' AND resource_id='$($script:versionID)'") -ne '1') { throw 'test request audit count is incorrect' }
    if ((Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='product.test.succeeded' AND resource_id='$($script:testID)'") -ne '1') { throw 'test success audit count is incorrect' }
    Assert-DbRejected "UPDATE app_product_version_tests SET attempts=attempts WHERE id='$($script:testID)'" 'completed product version tests are immutable'
    Assert-DbRejected "DELETE FROM app_product_version_tests WHERE id='$($script:testID)'" 'immutable history cannot be deleted'
    Assert-DbRejected 'BEGIN; TRUNCATE app_product_version_tests CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
    Wait-RuntimeAbsent $script:testContainer $script:testNetwork 30

    $first = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$($script:productID)/versions/$($script:versionID)/publish" -Headers $adminHeaders
    if ($first.published -ne $true -or $first.alreadyPublished -ne $false) { throw 'first publish response is not correct' }
    $second = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$($script:productID)/versions/$($script:versionID)/publish" -Headers $adminHeaders
    if ($second.published -ne $true -or $second.alreadyPublished -ne $true) { throw 'duplicate publish was not idempotent' }
    if ((Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='product.publish' AND resource_id='$($script:versionID)'") -ne '1') { throw 'duplicate publish wrote a second audit record' }
    if ((Get-Status POST "$Api/admin/products/$($script:productID)/versions/$($script:versionID)/tests" $adminHeaders $testBody) -ne 409) { throw 'published version was allowed to retest' }
    Assert-DbRejected "UPDATE app_product_versions SET id=gen_random_uuid() WHERE id='$($script:versionID)'" 'product version configuration is immutable'
    Assert-DbRejected "UPDATE app_product_versions SET published_at=NULL WHERE id='$($script:versionID)'" 'published product version cannot be unpublished'
    Assert-DbRejected "DELETE FROM app_product_versions WHERE id='$($script:versionID)'" 'immutable history cannot be deleted'
    Assert-DbRejected 'BEGIN; TRUNCATE app_product_versions CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
    if ((Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='product.version.create' AND resource_id='$($script:versionID)'") -ne '1') { throw 'product version creation audit count is incorrect' }

    $retired = Invoke-RestMethod -Method Patch -Uri "$Api/admin/products/$($script:productID)/availability" -Headers $adminHeaders -ContentType 'application/json' -Body '{"enabled":false}'
    $retiredReplay = Invoke-RestMethod -Method Patch -Uri "$Api/admin/products/$($script:productID)/availability" -Headers $adminHeaders -ContentType 'application/json' -Body '{"enabled":false}'
    if ($retired.status -ne 'retired' -or $retired.idempotent -ne $false -or $retiredReplay.idempotent -ne $true) { throw 'product retirement was not idempotent' }
    $retiredCatalog = Invoke-RestMethod -Method Get -Uri "$Api/products" -Headers $adminHeaders
    if (@($retiredCatalog.products | Where-Object { $_.id -eq $script:productID }).Count -ne 0) { throw 'retired product remained in the user catalog' }
    if ((Get-Status POST "$Api/admin/products/$($script:productID)/versions" $adminHeaders $versionBody) -ne 409) { throw 'retired product accepted a new version' }

    $restored = Invoke-RestMethod -Method Patch -Uri "$Api/admin/products/$($script:productID)/availability" -Headers $adminHeaders -ContentType 'application/json' -Body '{"enabled":true}'
    $restoredReplay = Invoke-RestMethod -Method Patch -Uri "$Api/admin/products/$($script:productID)/availability" -Headers $adminHeaders -ContentType 'application/json' -Body '{"enabled":true}'
    if ($restored.status -ne 'published' -or $restored.idempotent -ne $false -or $restoredReplay.idempotent -ne $true) { throw 'published product restoration was not idempotent' }
    $restoredCatalog = Invoke-RestMethod -Method Get -Uri "$Api/products" -Headers $adminHeaders
    if (@($restoredCatalog.products | Where-Object { $_.id -eq $script:productID -and $_.versionId -eq $script:versionID }).Count -ne 1) { throw 'restored product did not return to the user catalog' }
    if ((Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='product.availability.update' AND resource_id='$($script:productID)'") -ne '2') { throw 'product availability audit count is incorrect' }

    $orphanID = [guid]::NewGuid().ToString()
    $script:orphanContainer = "cm-test-$RuntimeScope-$orphanID"
    $script:orphanProbe = "cm-test-health-$RuntimeScope-$orphanID"
    $script:orphanNetwork = "cm-test-net-$RuntimeScope-$($orphanID.Replace('-', ''))"
    & docker network create --internal --label "cloudmeter.managed=true" --label "cloudmeter.owner=$RuntimeOwner" $script:orphanNetwork | Out-Null
    & docker create --name $script:orphanContainer --label "cloudmeter.managed=true" --label "cloudmeter.owner=$RuntimeOwner" --network $script:orphanNetwork $Image | Out-Null
    & docker create --name $script:orphanProbe --label "cloudmeter.managed=true" --label "cloudmeter.owner=$RuntimeOwner" --network $script:orphanNetwork $Image | Out-Null
    Wait-RuntimeAbsent $script:orphanContainer $script:orphanNetwork 30
    if (Test-DockerObjectExists @('container', 'inspect', $script:orphanProbe)) { throw 'orphan health probe was not reclaimed' }

    Write-Host 'Product lifecycle, pre-publish test, encrypted secret, isolated runtime, immutable snapshot, idempotent publish, audit and orphan cleanup verification passed'
} finally {
    try { Start-Worker } catch { }
    if ($script:testContainer) { Invoke-DockerCleanup @('rm', '-f', $script:testContainer) }
    if ($script:testID) {
        Invoke-DockerCleanup @('rm', '-f', "cm-test-health-$RuntimeScope-$($script:testID)")
        $compact = $script:testID.Replace('-', '')
        Invoke-DockerCleanup @('rm', '-f', "cm-test-health-$RuntimeScope-$($compact.Substring(0, 12))")
    }
    if ($script:testNetwork) { Invoke-DockerCleanup @('network', 'rm', $script:testNetwork) }
    if ($script:orphanContainer) { Invoke-DockerCleanup @('rm', '-f', $script:orphanContainer) }
    if ($script:orphanProbe) { Invoke-DockerCleanup @('rm', '-f', $script:orphanProbe) }
    if ($script:orphanNetwork) { Invoke-DockerCleanup @('network', 'rm', $script:orphanNetwork) }
    Invoke-DbQuiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$adminSessionID'"
    if ($script:productID) {
        Invoke-DbQuiet "UPDATE app_products SET status='retired' WHERE id='$($script:productID)'"
    }
}
