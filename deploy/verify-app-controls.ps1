$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$Compose = @('docker', 'compose')
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @('--project-name', $env:COMPOSE_PROJECT_NAME) }
$Compose += @('--env-file', '.env', '-f', 'deploy/compose.yaml')
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$Port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$Api = "http://127.0.0.1:$Port/api"
$BaseUrl = "http://127.0.0.1:$Port"
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

function New-Session([string]$UserID) {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $token = ([Convert]::ToBase64String($bytes)).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    $id = Invoke-Db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$UserID',digest('$token','sha256'),now()+interval '30 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}

function Invoke-Json([string]$Method, [string]$Uri, [hashtable]$Headers = $null, [string]$Body = '') {
    $params = @{ UseBasicParsing = $true; Method = $Method; Uri = $Uri }
    if ($Headers) { $params.Headers = $Headers }
    if ($Body) { $params.ContentType = 'application/json'; $params.Body = $Body }
    try {
        $response = Invoke-WebRequest @params
        $content = $null
        if ($response.Content) {
            try { $content = $response.Content | ConvertFrom-Json -ErrorAction Stop }
            catch { $content = $response.Content }
        }
        return [pscustomobject]@{ StatusCode = [int]$response.StatusCode; Body = $content; ErrorCode = '' }
    } catch {
        if (-not $_.Exception.Response) { throw }
        $code = ''
        try { $code = (($_.ErrorDetails.Message | ConvertFrom-Json).error.code) } catch { }
        return [pscustomobject]@{ StatusCode = [int]$_.Exception.Response.StatusCode; Body = $null; ErrorCode = $code }
    }
}

function Wait-Db([string]$Query, [string]$Expected, [int]$Seconds = 150) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $value = ''
    do {
        $value = Invoke-Db $Query
        if ($value -eq $Expected) { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for '$Expected'; last value was '$value'"
}

function Wait-Job([string]$JobID, [int]$Seconds = 180) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $state = ''
    do {
        $state = Invoke-Db "SELECT state FROM deployment_jobs WHERE id='$JobID'"
        if ($state -eq 'succeeded') { return }
        if ($state -eq 'failed') {
            $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$JobID'"
            throw "deployment failed: $lastError"
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for deployment $JobID; last state was $state"
}

function Wait-Backup([string]$BackupID, [int]$Seconds = 180) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $state = ''
    do {
        $state = Invoke-Db "SELECT status FROM app_backups WHERE id='$BackupID'"
        if ($state -eq 'succeeded') { return }
        if ($state -eq 'failed') {
            $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM app_backups WHERE id='$BackupID'"
            throw "backup failed: $lastError"
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for backup $BackupID; last state was $state"
}

function Wait-Restore([string]$RestoreID, [int]$Seconds = 180) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $state = ''
    do {
        $state = Invoke-Db "SELECT status FROM app_restore_jobs WHERE id='$RestoreID'"
        if ($state -eq 'succeeded') { return }
        if ($state -eq 'failed') {
            $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM app_restore_jobs WHERE id='$RestoreID'"
            throw "restore failed: $lastError"
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for restore $RestoreID; last state was $state"
}

function Wait-ProductTest([string]$TestID, [int]$Seconds = 180) {
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

function Wait-HttpStatus([string]$Uri, [int]$Expected, [int]$Seconds = 45) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $lastStatus = 0
    do {
        $lastStatus = (Invoke-Json GET $Uri).StatusCode
        if ($lastStatus -eq $Expected) { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for HTTP $Expected at $Uri; last status was $lastStatus"
}

function Stop-Worker {
    if (-not $script:WorkerStopped) {
        & $Compose[0] $Compose[1..($Compose.Length - 1)] stop worker | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'failed to stop worker' }
        $script:WorkerStopped = $true
    }
}

function Start-Worker {
    if ($script:WorkerStopped) {
        & $Compose[0] $Compose[1..($Compose.Length - 1)] start worker | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'failed to start worker' }
        $script:WorkerStopped = $false
    }
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

function Get-RuntimeScope([string]$Owner) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $hash = $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Owner.Trim())) } finally { $sha.Dispose() }
    return (($hash[0..4] | ForEach-Object { $_.ToString('x2') }) -join '')
}

function Get-AppVolumeName([string]$Owner, [string]$AppID, [string]$Key) {
    $compact = $AppID.Replace('-', '')
    if ($compact.Length -gt 20) { $compact = $compact.Substring(0, 20) }
    if ($Owner.Trim().ToLowerInvariant() -eq 'cloudmeter') { return "cmv-$compact-$Key" }
    return "cmv-$(Get-RuntimeScope $Owner)-$compact-$Key"
}

function Get-BackupVolumeName([string]$Owner, [string]$Configured) {
    $name = $Configured.Trim()
    if (-not $name) { $name = 'cloudmeter_backup_data' }
    if ($Owner.Trim().ToLowerInvariant() -eq 'cloudmeter') { return $name }
    $scope = Get-RuntimeScope $Owner
    if ($name -eq 'cloudmeter_backup_data') { return "cloudmeter_backup_$scope" }
    if ($name.EndsWith("-$scope") -or $name.EndsWith("_$scope")) { return $name }
    return "$name-$scope"
}

$workerID = (& $Compose[0] $Compose[1..($Compose.Length - 1)] ps -q worker).Trim()
if (-not $workerID) { throw 'worker must be running' }
$workerEnv = @(& docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' $workerID)
if ($workerEnv -notcontains 'DOCKER_EXECUTOR_ENABLED=true') { throw 'application control verification requires DOCKER_EXECUTOR_ENABLED=true' }
$runtimeOwnerLine = $workerEnv | Where-Object { $_ -like 'RUNTIME_OWNER=*' } | Select-Object -First 1
if (-not $runtimeOwnerLine) { throw 'worker must expose RUNTIME_OWNER for scoped verification' }
$RuntimeOwner = $runtimeOwnerLine.Substring('RUNTIME_OWNER='.Length).Trim()
$RuntimeScope = Get-RuntimeScope $RuntimeOwner
$backupVolumeLine = $workerEnv | Where-Object { $_ -like 'BACKUP_STORAGE_VOLUME=*' } | Select-Object -First 1
if (-not $backupVolumeLine) { throw 'worker must expose BACKUP_STORAGE_VOLUME for scoped verification' }
$BackupVolume = Get-BackupVolumeName $RuntimeOwner $backupVolumeLine.Substring('BACKUP_STORAGE_VOLUME='.Length)
& docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) { throw 'fixed verification image is not available' }
if ((Invoke-Db "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations") -ne "$LatestMigration|clean") { throw "migration $LatestMigration must be applied before verification" }

$adminID = Invoke-Db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID) { throw 'an active super administrator is required' }
$adminSession = New-Session $adminID
$adminHeaders = @{ Authorization = "Bearer $($adminSession.Token)" }
$marker = [guid]::NewGuid().ToString('N').Substring(0, 12)
$password = "Lifecycle-$marker-Password!"
$userID = ''
$userSession = $null
$planID = ''
$productID = ''
$appID = ''
$volume = ''
$backupID = ''
$backupStorageKey = ''
$restoreID = ''
$script:WorkerStopped = $false

try {
    $product = (Invoke-Json POST "$Api/admin/products" $adminHeaders (@{ slug = "verify-control-$marker"; name = "Application control $marker" } | ConvertTo-Json -Compress))
    if ($product.StatusCode -ne 201) { throw "product creation failed: $($product.StatusCode) $($product.ErrorCode)" }
    $productID = $product.Body.id
    $versionBody = @{
        imageDigest = $Image
        runtimeSpec = @{
            cpuCores = 0.25; memoryMiB = 128; systemDiskGiB = 1
            env = @{}; secretKeys = @('ROTATION_KEY'); dependencies = @()
            volumes = @(@{ name = 'data'; mountPath = '/data'; sizeGiB = 1 })
        }
        routeSpec = @{ containerPort = 80; basePath = '/'; stripPrefix = $true; websocket = $true; sse = $true; cookiePath = '/' }
        healthSpec = @{ path = '/'; intervalSeconds = 2; timeoutSeconds = 3 }
        updateSpec = @{ dataPolicy = 'volume_compatible' }
    } | ConvertTo-Json -Depth 10 -Compress
    $version = Invoke-Json POST "$Api/admin/products/$productID/versions" $adminHeaders $versionBody
    if ($version.StatusCode -ne 201) { throw "product version creation failed: $($version.StatusCode) $($version.ErrorCode)" }
    $versionID = $version.Body.id
    $test = Invoke-Json POST "$Api/admin/products/$productID/versions/$versionID/tests" $adminHeaders (@{ secrets = @{ ROTATION_KEY = "test-$marker" } } | ConvertTo-Json -Compress)
    if ($test.StatusCode -ne 202) { throw "product test queue failed: $($test.StatusCode) $($test.ErrorCode)" }
    Wait-ProductTest $test.Body.testId
    $publish = Invoke-Json POST "$Api/admin/products/$productID/versions/$versionID/publish" $adminHeaders
    if ($publish.StatusCode -ne 200 -or $publish.Body.published -ne $true) { throw 'product publication failed' }

    $plan = Invoke-Json POST "$Api/admin/plans" $adminHeaders (@{ code = "verify-control-$marker"; name = "Application control $marker" } | ConvertTo-Json -Compress)
    if ($plan.StatusCode -ne 201) { throw 'plan creation failed' }
    $planID = $plan.Body.id
    $planVersionBody = @{
        cyclePriceCents = 0; apps = 2; cpuCores = 2; memoryGiB = 2; dataDiskGiB = 2
        backupStorageGiB = 1; backupOperationsPerMonth = 2; concurrentDeployments = 2
        publicIngresses = 2; ingressOverageEnabled = $false; egressGiB = 1; egressOverageEnabled = $false
        creditGrantCents = 0; allowedProductIds = @($productID); effectiveAt = (Get-Date).ToUniversalTime().ToString('o')
    } | ConvertTo-Json -Compress
    $planVersion = Invoke-Json POST "$Api/admin/plans/$planID/versions" $adminHeaders $planVersionBody
    if ($planVersion.StatusCode -ne 201) { throw 'plan version creation failed' }

    $user = Invoke-Json POST "$Api/admin/users" $adminHeaders (@{ email = "control-$marker@example.invalid"; password = $password; displayName = 'Application control verification'; role = 'user' } | ConvertTo-Json -Compress)
    if ($user.StatusCode -ne 201) { throw 'user creation failed' }
    $userID = $user.Body.id
    $funding = Invoke-Json POST "$Api/admin/users/$userID/wallet/adjust" $adminHeaders (@{ amountCents = 100000; businessRef = "control-funding/$marker"; note = 'Application lifecycle verification funding' } | ConvertTo-Json -Compress)
    if ($funding.StatusCode -ne 200) { throw 'verification wallet funding failed' }
    $subscription = Invoke-Json PUT "$Api/admin/users/$userID/subscription" $adminHeaders (@{ planVersionId = $planVersion.Body.id; endsAt = $null } | ConvertTo-Json -Compress)
    if ($subscription.StatusCode -ne 200) { throw 'subscription assignment failed' }
    $userSession = New-Session $userID
    $userHeaders = @{ Authorization = "Bearer $($userSession.Token)" }

    $deployment = Invoke-Json POST "$Api/apps" $userHeaders (@{ productId = $productID; versionId = $versionID; slug = 'lifecycle'; idempotencyKey = "control-deploy/$marker"; secrets = @{ ROTATION_KEY = "initial-$marker" } } | ConvertTo-Json -Compress)
    if ($deployment.StatusCode -ne 201) { throw "application deployment failed to queue: $($deployment.StatusCode) $($deployment.ErrorCode)" }
    $appID = $deployment.Body.appId
    Wait-Job $deployment.Body.jobId
    Wait-Db "SELECT status FROM user_apps WHERE id='$appID'" 'running'
    if ((Invoke-Db "SELECT operation FROM deployment_jobs WHERE id='$($deployment.Body.jobId)'") -ne 'deploy') { throw 'initial deployment operation was not recorded' }
    $sourceRelease = Invoke-Db "SELECT last_successful_release_id FROM user_apps WHERE id='$appID'"
    Assert-DbRejected "UPDATE app_releases SET release_number=release_number+1000 WHERE id='$sourceRelease'" 'application release snapshot is immutable'
    Assert-DbRejected "DELETE FROM app_releases WHERE id='$sourceRelease'" 'application releases cannot be deleted'
    Assert-DbRejected 'BEGIN; TRUNCATE app_releases CASCADE; ROLLBACK;' 'immutable history cannot be truncated'

    $undeclaredSecret = Invoke-Json PUT "$Api/apps/$appID/secrets/LD_PRELOAD" $userHeaders (@{ value = "blocked-$marker" } | ConvertTo-Json -Compress)
    if ($undeclaredSecret.StatusCode -ne 400 -or $undeclaredSecret.ErrorCode -ne 'secret_not_declared') { throw 'undeclared application Secret was not rejected' }
    $firstSecret = Invoke-Json PUT "$Api/apps/$appID/secrets/ROTATION_KEY" $userHeaders (@{ value = "rotation-one-$marker" } | ConvertTo-Json -Compress)
    $secondSecret = Invoke-Json PUT "$Api/apps/$appID/secrets/ROTATION_KEY" $userHeaders (@{ value = "rotation-two-$marker" } | ConvertTo-Json -Compress)
    if ($firstSecret.StatusCode -ne 201 -or $firstSecret.Body.version -ne 2 -or $secondSecret.StatusCode -ne 201 -or $secondSecret.Body.version -ne 3) { throw 'application Secret rotation did not append versions' }
    $secretID = Invoke-Db "SELECT id FROM app_secrets WHERE user_app_id='$appID' AND key='ROTATION_KEY'"
    Assert-DbRejected "UPDATE app_secrets SET key='RENAMED_KEY' WHERE id='$secretID'" 'application Secret identity is immutable'
    Assert-DbRejected "DELETE FROM app_secrets WHERE id='$secretID'" 'application Secret records cannot be deleted'
    Assert-DbRejected 'BEGIN; TRUNCATE app_secrets CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
    $secretVersionID = Invoke-Db "SELECT v.id FROM app_secret_versions v JOIN app_secrets s ON s.id=v.app_secret_id WHERE s.user_app_id='$appID' AND s.key='ROTATION_KEY' ORDER BY v.version DESC LIMIT 1"
    Assert-DbRejected "UPDATE app_secret_versions SET encrypted_value=encrypted_value || 'changed' WHERE id='$secretVersionID'" 'application secret versions are immutable'
    Assert-DbRejected "DELETE FROM app_secret_versions WHERE id='$secretVersionID'" 'application secret versions are immutable'
    Assert-DbRejected 'BEGIN; TRUNCATE app_secret_versions CASCADE; ROLLBACK;' 'immutable history cannot be truncated'

    $container = Invoke-Db "SELECT upstream_container FROM app_routes WHERE user_app_id='$appID'"
    $publicPath = Invoke-Db "SELECT public_path FROM app_routes WHERE user_app_id='$appID'"
    if (-not $container -or -not (Test-DockerObjectExists @('container', 'inspect', $container))) { throw "runtime container was not created: [$container]" }
    if ((Invoke-Json GET "$BaseUrl$publicPath").StatusCode -ne 200) { throw 'running application route is unavailable' }
    Assert-DbRejected "UPDATE users SET slug=slug || '-changed' WHERE id='$userID'" 'user public identity is immutable'
    Assert-DbRejected "UPDATE user_apps SET slug=slug || '-changed' WHERE id='$appID'" 'user application identity is immutable'
    Assert-DbRejected "INSERT INTO users(email,password_hash,display_name,slug) VALUES('invalid-slug-$marker@example.invalid','invalid','Invalid slug','INVALID_$marker')" 'users_slug_format_check'
    Assert-DbRejected "INSERT INTO user_apps(user_id,product_id,slug,service_slug,status) VALUES('$userID','$productID','INVALID_$marker','format-$marker','stopped')" 'user_apps_slug_format_check'
    Assert-DbRejected "INSERT INTO user_apps(user_id,product_id,slug,service_slug,status) VALUES('$userID','$productID','format-$marker','INVALID_$marker','stopped')" 'user_apps_service_slug_format_check'
    Assert-DbRejected "INSERT INTO user_apps(user_id,product_id,slug,service_slug,status,last_successful_release_id) VALUES('$userID','$productID','foreign-$marker','foreign-$marker','running','$sourceRelease')" 'last successful release must belong to the same application'
    Assert-DbRejected "UPDATE app_routes SET public_path='/apps/hijacked/path' WHERE user_app_id='$appID'" 'application route public path does not match application identity'
    Assert-DbRejected "UPDATE app_routes SET upstream_host='release-000000000000' WHERE user_app_id='$appID'" 'application route upstream host does not match release identity'
    Assert-DbRejected "UPDATE app_routes SET upstream_port=upstream_port+1 WHERE user_app_id='$appID'" 'application route upstream port does not match release snapshot'
    Assert-DbRejected "UPDATE app_routes SET upstream_container='cm-00000000000-$appID-$sourceRelease' WHERE user_app_id='$appID'" 'application route container does not match instance and release identity'
    & $Compose[0] $Compose[1..($Compose.Length - 1)] up -d --no-build --force-recreate --no-deps app-router | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'failed to recreate the application router' }
    Wait-HttpStatus "$BaseUrl$publicPath" 200
    $volume = Get-AppVolumeName $RuntimeOwner $appID 'data'
    if (-not (Test-DockerObjectExists @('volume', 'inspect', $volume))) { throw 'persistent application volume was not created' }
    $volumeOwner = ((& docker volume inspect --format '{{json .Labels}}' $volume) | ConvertFrom-Json).'cloudmeter.owner'
    if ($volumeOwner -ne $RuntimeOwner) { throw 'persistent application volume is not owned by this runtime' }
    & docker exec $container sh -c "printf '$marker' > /data/lifecycle-marker"
    if ($LASTEXITCODE -ne 0) { throw 'persistent volume marker write failed' }

    $backup = Invoke-Json POST "$Api/apps/$appID/backups" $userHeaders (@{ volumeKey = 'data' } | ConvertTo-Json -Compress)
    if ($backup.StatusCode -ne 202 -or -not $backup.Body.backupId) { throw "backup request was not accepted: $($backup.StatusCode) $($backup.ErrorCode)" }
    $backupID = $backup.Body.backupId
    Wait-Backup $backupID
    if ((Invoke-Db "SELECT docker_volume FROM app_backups WHERE id='$backupID'") -ne $volume) { throw 'backup did not retain the owner-scoped application volume name' }
    $backupStorageKey = Invoke-Db "SELECT storage_key FROM app_backups WHERE id='$backupID'"
    if (-not $backupStorageKey -or [int64](Invoke-Db "SELECT coalesce(size_bytes,0) FROM app_backups WHERE id='$backupID'") -le 0) { throw 'backup did not retain an archive with a positive size' }
    if (-not (Test-DockerObjectExists @('volume', 'inspect', $BackupVolume))) { throw 'owner-scoped backup volume was not created' }
    $backupVolumeOwner = ((& docker volume inspect --format '{{json .Labels}}' $BackupVolume) | ConvertFrom-Json).'cloudmeter.owner'
    $legacyProductionVolume = (-not $backupVolumeOwner -and $RuntimeOwner.Trim().ToLowerInvariant() -eq 'cloudmeter')
    if ($backupVolumeOwner -ne $RuntimeOwner -and -not $legacyProductionVolume) { throw 'backup volume is not owned by this runtime' }
    & docker run --rm -v "${BackupVolume}:/backup:ro" $Image sh -c "test -s /backup/$backupStorageKey" *> $null
    if ($LASTEXITCODE -ne 0) { throw 'backup archive was not written to the owner-scoped backup volume' }
    if (Test-DockerObjectExists @('container', 'inspect', "cm-backup-$RuntimeScope-$backupID")) { throw 'backup helper container was not removed' }

    & docker exec $container sh -c "printf 'changed-$marker' > /data/lifecycle-marker"
    if ($LASTEXITCODE -ne 0) { throw 'persistent volume marker mutation failed before restore' }
    $restore = Invoke-Json POST "$Api/apps/$appID/backups/$backupID/restore" $userHeaders (@{ idempotencyKey = "control-restore/$marker" } | ConvertTo-Json -Compress)
    if ($restore.StatusCode -ne 202 -or -not $restore.Body.restoreJobId) { throw "restore request was not accepted: $($restore.StatusCode) $($restore.ErrorCode)" }
    $restoreID = $restore.Body.restoreJobId
    Wait-Restore $restoreID
    Wait-Db "SELECT status FROM user_apps WHERE id='$appID'" 'running'
    $restoredMarker = ((& docker exec $container cat /data/lifecycle-marker) -as [string]).Trim()
    if ($restoredMarker -ne $marker) { throw 'backup restore did not recover the persistent volume contents' }
    if (Test-DockerObjectExists @('container', 'inspect', "cm-restore-$RuntimeScope-$restoreID")) { throw 'restore helper container was not removed' }
    Assert-DbRejected "UPDATE app_backups SET storage_key=storage_key || '.changed' WHERE id='$backupID'" 'application backup identity is immutable'
    Assert-DbRejected "UPDATE app_backups SET size_bytes=size_bytes+1 WHERE id='$backupID'" 'completed application backups are immutable'
    Assert-DbRejected "DELETE FROM app_backups WHERE id='$backupID'" 'immutable history cannot be deleted'
    Assert-DbRejected 'BEGIN; TRUNCATE app_backups CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
    Assert-DbRejected "UPDATE app_restore_jobs SET idempotency_key=idempotency_key || '-changed' WHERE id='$restoreID'" 'application restore job identity is immutable'
    Assert-DbRejected "UPDATE app_restore_jobs SET last_error='changed' WHERE id='$restoreID'" 'completed application restore jobs are immutable'
    Assert-DbRejected "DELETE FROM app_restore_jobs WHERE id='$restoreID'" 'immutable history cannot be deleted'
    Assert-DbRejected 'BEGIN; TRUNCATE app_restore_jobs; ROLLBACK;' 'immutable history cannot be truncated'
    Assert-DbRejected "INSERT INTO app_restore_jobs(backup_id,user_app_id,idempotency_key) VALUES('$backupID',gen_random_uuid(),'foreign-$marker')" 'application restore job must reference a successful backup of the same application'
    $deploymentChargeCount = Invoke-Db "SELECT count(*) FROM usage_events WHERE user_app_id='$appID' AND usage_code='app.deployment'"

    Stop-Worker
    $stopKey = "control-stop/$marker"
    $stop = Invoke-Json POST "$Api/apps/$appID/stop" $userHeaders (@{ idempotencyKey = $stopKey } | ConvertTo-Json -Compress)
    if ($stop.StatusCode -ne 202 -or $stop.Body.status -ne 'stopping') { throw 'stop request was not accepted' }
    if ((Invoke-Db "SELECT status FROM user_apps WHERE id='$appID'") -ne 'stopping') { throw 'application did not enter stopping state' }
    if ((Invoke-Db "SELECT count(*) FROM app_routes WHERE user_app_id='$appID'") -ne '0') { throw 'public route was not removed atomically' }
    if ((Invoke-Json GET "$BaseUrl$publicPath").StatusCode -ne 404) { throw 'stopped application route did not return 404 immediately' }
    if (-not (Test-DockerObjectExists @('container', 'inspect', $container))) { throw 'container disappeared before the queued stop task ran' }
    $stopReplay = Invoke-Json POST "$Api/apps/$appID/stop" $userHeaders (@{ idempotencyKey = $stopKey } | ConvertTo-Json -Compress)
    if ($stopReplay.StatusCode -ne 200 -or $stopReplay.Body.stopJobId -ne $stop.Body.stopJobId) { throw 'stop idempotency replay failed' }
    if ((Invoke-Db "SELECT count(*) FROM app_stop_jobs WHERE user_app_id='$appID' AND idempotency_key='$stopKey'") -ne '1') { throw 'stop replay created a duplicate job' }

    Start-Worker
    Wait-Db "SELECT status FROM user_apps WHERE id='$appID'" 'stopped'
    Wait-Db "SELECT status FROM app_stop_jobs WHERE id='$($stop.Body.stopJobId)'" 'succeeded'
    if (Test-DockerObjectExists @('container', 'inspect', $container)) { throw 'stopped application container still exists' }
    if (-not (Test-DockerObjectExists @('volume', 'inspect', $volume))) { throw 'persistent volume was removed while stopping' }

    $updateWhileStopped = Invoke-Json POST "$Api/apps/$appID/releases" $userHeaders (@{ versionId = $versionID; idempotencyKey = "control-stopped-update/$marker" } | ConvertTo-Json -Compress)
    if ($updateWhileStopped.StatusCode -ne 409 -or $updateWhileStopped.ErrorCode -ne 'app_not_running') { throw 'stopped application accepted an update' }
    $rollbackWhileStopped = Invoke-Json POST "$Api/apps/$appID/rollback" $userHeaders (@{ releaseId = $sourceRelease; idempotencyKey = "control-stopped-rollback/$marker" } | ConvertTo-Json -Compress)
    if ($rollbackWhileStopped.StatusCode -ne 409 -or $rollbackWhileStopped.ErrorCode -ne 'app_not_running') { throw 'stopped application accepted a rollback' }

    $startKey = "control-start/$marker"
    $start = Invoke-Json POST "$Api/apps/$appID/start" $userHeaders (@{ idempotencyKey = $startKey } | ConvertTo-Json -Compress)
    if ($start.StatusCode -ne 202) { throw "start request was not accepted: $($start.StatusCode) $($start.ErrorCode)" }
    if ($start.Body.releaseId -eq $sourceRelease) { throw 'start did not create a new immutable release' }
    Wait-Job $start.Body.jobId
    Wait-Db "SELECT status FROM user_apps WHERE id='$appID'" 'running'
    $jobMetadata = Invoke-Db "SELECT operation || '|' || source_release_id::text FROM deployment_jobs WHERE id='$($start.Body.jobId)'"
    if ($jobMetadata -ne "start|$sourceRelease") { throw "start job metadata is incorrect: $jobMetadata" }
    if ((Invoke-Db "SELECT count(*) FROM app_releases WHERE user_app_id='$appID'") -ne '2') { throw 'start did not append exactly one release' }
    if ((Invoke-Db "SELECT count(*) FROM usage_events WHERE user_app_id='$appID' AND usage_code='app.deployment'") -ne $deploymentChargeCount) { throw 'start incorrectly generated a deployment fee event' }
    $newContainer = Invoke-Db "SELECT upstream_container FROM app_routes WHERE user_app_id='$appID'"
    if (-not $newContainer -or $newContainer -eq $container -or -not (Test-DockerObjectExists @('container', 'inspect', $newContainer))) { throw 'start did not create a new runtime container' }
    $storedMarker = ((& docker exec $newContainer cat /data/lifecycle-marker) -as [string]).Trim()
    if ($storedMarker -ne $marker) { throw 'persistent volume contents were not preserved across stop and start' }
    if ((Invoke-Json GET "$BaseUrl$publicPath").StatusCode -ne 200) { throw 'application route was not restored after start' }
    $startReplay = Invoke-Json POST "$Api/apps/$appID/start" $userHeaders (@{ idempotencyKey = $startKey } | ConvertTo-Json -Compress)
    if ($startReplay.StatusCode -ne 200 -or $startReplay.Body.jobId -ne $start.Body.jobId) { throw 'start idempotency replay failed' }

    $finalStop = Invoke-Json POST "$Api/apps/$appID/stop" $userHeaders (@{ idempotencyKey = "control-final-stop/$marker" } | ConvertTo-Json -Compress)
    if ($finalStop.StatusCode -ne 202) { throw 'final stop request failed' }
    Wait-Db "SELECT status FROM user_apps WHERE id='$appID'" 'stopped'
    Invoke-Db "UPDATE user_apps SET status='suspended',suspension_reason='billing_insufficient' WHERE id='$appID'" | Out-Null
    foreach ($operation in @('start', 'update', 'rollback')) {
        if ($operation -eq 'start') {
            $blocked = Invoke-Json POST "$Api/apps/$appID/start" $userHeaders (@{ idempotencyKey = "control-blocked-start/$marker" } | ConvertTo-Json -Compress)
        } elseif ($operation -eq 'update') {
            $blocked = Invoke-Json POST "$Api/apps/$appID/releases" $userHeaders (@{ versionId = $versionID; idempotencyKey = "control-blocked-update/$marker" } | ConvertTo-Json -Compress)
        } else {
            $blocked = Invoke-Json POST "$Api/apps/$appID/rollback" $userHeaders (@{ releaseId = $sourceRelease; idempotencyKey = "control-blocked-rollback/$marker" } | ConvertTo-Json -Compress)
        }
        if ($blocked.StatusCode -ne 409 -or $blocked.ErrorCode -ne 'app_suspended') { throw "platform-suspended application accepted $operation" }
    }

    Write-Host 'Application backup/restore history integrity, immutable Release and Secret history, owner-scoped storage, helper cleanup, stop/start route removal, idempotency, persistent volume retention, lifecycle guards and no-charge restart verification passed'
} finally {
    try { Start-Worker } catch { }
    if ($backupStorageKey -and $BackupVolume) {
        try { & docker run --rm -v "${BackupVolume}:/backup" $Image sh -c "rm -f -- /backup/$backupStorageKey" *> $null } catch { }
    }
    if ($userSession) { Invoke-DbQuiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($userSession.ID)'" }
    Invoke-DbQuiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($adminSession.ID)'"
    if ($appID) {
        Invoke-DbQuiet "UPDATE deployment_jobs SET state='failed',last_error=coalesce(last_error,'application control verification cleanup'),updated_at=now() WHERE user_app_id='$appID' AND state NOT IN ('succeeded','failed'); DELETE FROM app_routes WHERE user_app_id='$appID'; UPDATE user_apps SET status='suspended',suspension_reason='billing_insufficient' WHERE id='$appID'"
        $containers = @(& docker ps -a --filter "label=cloudmeter.owner=$RuntimeOwner" --filter "label=cloudmeter.app_id=$appID" --format '{{.Names}}')
        foreach ($runtimeContainer in $containers) { try { & docker rm -f $runtimeContainer *> $null } catch { } }
    }
    if ($userID) { Invoke-DbQuiet "UPDATE users SET status='suspended',updated_at=now() WHERE id='$userID'" }
    if ($planID) { Invoke-DbQuiet "UPDATE plans SET purchase_enabled=false WHERE id='$planID'" }
    if ($productID) { Invoke-DbQuiet "UPDATE app_products SET status='retired' WHERE id='$productID'" }
}
