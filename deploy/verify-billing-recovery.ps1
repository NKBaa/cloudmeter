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

function Wait-Db([string]$Query, [string]$Expected, [int]$Seconds = 150) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $value = ''
    do {
        $value = Invoke-Db $Query
        if ($value -eq $Expected) { return }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for '$Expected'; last value was '$value'"
}

function Wait-Deployment([string]$JobID, [int]$Seconds = 180) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $state = ''
    do {
        $state = Invoke-Db "SELECT state FROM deployment_jobs WHERE id='$JobID'"
        if ($state -eq 'succeeded') { return }
        if ($state -eq 'failed') {
            $lastError = Invoke-Db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$JobID'"
            throw "deployment failed: $lastError"
        }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for deployment $JobID; last state was $state"
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
        Start-Sleep -Seconds 1
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

function Remove-TestNetwork([string]$Name, [string]$Owner) {
    if (-not $Name) { return }
    $labels = (& docker network inspect --format '{{json .Labels}}' $Name 2>$null) -as [string]
    if ($LASTEXITCODE -ne 0 -or -not $labels) { return }
    $networkOwner = (($labels | ConvertFrom-Json).'cloudmeter.owner') -as [string]
    if ($networkOwner -ne $Owner) { return }
    $members = @(& docker network inspect --format '{{range .Containers}}{{println .Name}}{{end}}' $Name 2>$null) | Where-Object { $_ }
    foreach ($member in $members) { try { & docker network disconnect -f $Name $member *> $null } catch { } }
    try { & docker network rm $Name *> $null } catch { }
}

$WorkerID = (& $Compose[0] $Compose[1..($Compose.Length - 1)] ps -q worker).Trim()
if (-not $WorkerID) { throw 'worker must be running' }
$WorkerEnv = @(& docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' $WorkerID)
if ($WorkerEnv -notcontains 'DOCKER_EXECUTOR_ENABLED=true') { throw 'billing recovery verification requires the Docker executor' }
$RuntimeOwnerLine = $WorkerEnv | Where-Object { $_ -like 'RUNTIME_OWNER=*' } | Select-Object -First 1
if (-not $RuntimeOwnerLine) { throw 'worker must expose RUNTIME_OWNER' }
$RuntimeOwner = $RuntimeOwnerLine.Substring('RUNTIME_OWNER='.Length).Trim()
& docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) { throw 'fixed verification image is not available' }

$AdminID = Invoke-Db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $AdminID) { throw 'an active super administrator is required' }
$AdminSession = New-Session $AdminID
$AdminHeaders = @{ Authorization = "Bearer $($AdminSession.Token)" }
$Marker = [guid]::NewGuid().ToString('N').Substring(0, 12)
$Password = "Billing-$Marker-Password!"
$UsageCode = 'verify.recovery.units'
$UnpricedCode = "verify.unpriced.$Marker"
$UserID = ''
$UserSession = $null
$ProductID = ''
$PlanID = ''
$AppID = ''
$OverrideID = ''
$Network = ''

try {
    $product = Invoke-RestMethod -Method Post -Uri "$Api/admin/products" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ slug = "verify-billing-$Marker"; name = "Billing recovery $Marker" } | ConvertTo-Json -Compress)
    $ProductID = $product.id
    $versionBody = @{
        imageDigest = $Image
        runtimeSpec = @{ cpuCores = 0.25; memoryMiB = 128; systemDiskGiB = 1; env = @{}; secretKeys = @(); dependencies = @(); volumes = @() }
        routeSpec = @{ containerPort = 80; basePath = '/'; stripPrefix = $true; websocket = $true; sse = $true; cookiePath = '/' }
        healthSpec = @{ path = '/'; intervalSeconds = 2; timeoutSeconds = 3 }
        updateSpec = @{ dataPolicy = 'stateless' }
    } | ConvertTo-Json -Depth 10 -Compress
    $version = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$ProductID/versions" -Headers $AdminHeaders -ContentType 'application/json' -Body $versionBody
    $test = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$ProductID/versions/$($version.id)/tests" -Headers $AdminHeaders -ContentType 'application/json' -Body '{"secrets":{}}'
    Wait-ProductTest $test.testId
    $published = Invoke-RestMethod -Method Post -Uri "$Api/admin/products/$ProductID/versions/$($version.id)/publish" -Headers $AdminHeaders
    if (-not $published.published) { throw 'product publication failed' }

    $plan = Invoke-RestMethod -Method Post -Uri "$Api/admin/plans" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ code = "verify-billing-$Marker"; name = "Billing recovery $Marker" } | ConvertTo-Json -Compress)
    $PlanID = $plan.id
    $planVersion = Invoke-RestMethod -Method Post -Uri "$Api/admin/plans/$PlanID/versions" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{
        cyclePriceCents = 0; apps = 1; cpuCores = 1; memoryGiB = 1; dataDiskGiB = 0
        backupStorageGiB = 0; backupOperationsPerMonth = 0; concurrentDeployments = 1
        publicIngresses = 1; ingressOverageEnabled = $false; egressGiB = 1; egressOverageEnabled = $false
        creditGrantCents = 0; allowedProductIds = @($ProductID); effectiveAt = (Get-Date).ToUniversalTime().ToString('o')
    } | ConvertTo-Json -Compress)

    $user = Invoke-RestMethod -Method Post -Uri "$Api/admin/users" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ email = "billing-$Marker@example.invalid"; password = $Password; displayName = 'Billing recovery verification'; role = 'user' } | ConvertTo-Json -Compress)
    $UserID = $user.id
    $Network = Get-UserNetworkName $RuntimeOwner $UserID
    Invoke-RestMethod -Method Put -Uri "$Api/admin/users/$UserID/subscription" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ planVersionId = $planVersion.id; endsAt = $null } | ConvertTo-Json -Compress) | Out-Null
    $UserSession = New-Session $UserID
    $UserHeaders = @{ Authorization = "Bearer $($UserSession.Token)" }

    $pricingCatalog = Invoke-RestMethod -Method Get -Uri "$Api/admin/pricing" -Headers $AdminHeaders
    $item = $pricingCatalog.items | Where-Object { $_.code -eq $UsageCode -and $_.unit -eq 'unit' } | Select-Object -First 1
    if ($null -eq $item) {
        $item = Invoke-RestMethod -Method Post -Uri "$Api/admin/pricing/items" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ code = $UsageCode; unit = 'unit' } | ConvertTo-Json -Compress)
    }
    $now = (Get-Date).ToUniversalTime()
    $priceVersion = $item.versions | Where-Object {
        [int64]$_.unitPriceMicros -eq 1000000 -and [int]$_.precisionScale -eq 6 -and
        $_.roundingMode -eq 'half_up' -and [decimal]$_.minimumQuantity -eq 0 -and
        [decimal]$_.freeQuantity -eq 0 -and ([DateTime]$_.effectiveAt).ToUniversalTime() -le $now
    } | Sort-Object { [DateTime]$_.effectiveAt } -Descending | Select-Object -First 1
    if ($null -eq $priceVersion) {
        $priceVersion = Invoke-RestMethod -Method Post -Uri "$Api/admin/pricing/items/$($item.id)/versions" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ unitPriceMicros = 1000000; precisionScale = 6; roundingMode = 'half_up'; minimumQuantity = '0'; freeQuantity = '0'; effectiveAt = $now.AddDays(-1).ToString('o') } | ConvertTo-Json -Compress)
    }
    $override = Invoke-RestMethod -Method Put -Uri "$Api/admin/pricing/overrides" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ pricingItemId = $item.id; pricingVersionId = $priceVersion.id; scope = 'user'; scopeId = $UserID } | ConvertTo-Json -Compress)
    $OverrideID = $override.id

    $deployment = Invoke-RestMethod -Method Post -Uri "$Api/apps" -Headers $UserHeaders -ContentType 'application/json' -Body (@{ productId = $ProductID; versionId = $version.id; slug = 'billing'; idempotencyKey = "billing-deploy/$Marker"; secrets = @{} } | ConvertTo-Json -Compress)
    $AppID = $deployment.appId
    Wait-Deployment $deployment.jobId
    Wait-Db "SELECT status FROM user_apps WHERE id='$AppID'" 'running'
    Wait-Db "SELECT count(*) FROM app_routes WHERE user_app_id='$AppID'" '1'
    $container = Invoke-Db "SELECT upstream_container FROM app_routes WHERE user_app_id='$AppID'"
    if (-not $container -or -not (& docker inspect $container 2>$null)) { throw 'runtime container was not created' }

    $seedOrder = Invoke-RestMethod -Method Post -Uri "$Api/payments/orders" -Headers $UserHeaders -ContentType 'application/json' -Body (@{ amountCents = 100; provider = 'manual'; idempotencyKey = "billing-seed/$Marker" } | ConvertTo-Json -Compress)
    Invoke-RestMethod -Method Post -Uri "$Api/admin/payments/orders/$($seedOrder.orderId)/mark-paid" -Headers $AdminHeaders -ContentType 'application/json' -Body '{}' | Out-Null
    Wait-Db "SELECT balance_cents FROM wallets WHERE user_id='$UserID'" '100'

    Invoke-Db "UPDATE usage_aggregates SET sealed_at=now(),billing_disposition='waived_legacy' WHERE user_id='$UserID' AND billing_disposition='pending'" | Out-Null
    $LowStart = (Get-Date).ToUniversalTime().AddMinutes(-22).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    $LowEnd = (Get-Date).ToUniversalTime().AddMinutes(-21).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    Invoke-Db "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$UserID','$AppID','$UsageCode',1,'unit','$LowStart','$LowEnd','$($priceVersion.id)','billing-low/$Marker')" | Out-Null
    Wait-Db "SELECT balance_cents FROM wallets WHERE user_id='$UserID'" '99'
    Wait-Db "SELECT count(*) FROM user_notifications n JOIN usage_aggregates a ON n.event_key='low-balance/'||a.id::text WHERE n.user_id='$UserID' AND a.usage_code='$UsageCode' AND a.window_start='$LowStart'" '1'

    Invoke-Db "UPDATE usage_aggregates SET sealed_at=now(),billing_disposition='waived_legacy' WHERE user_id='$UserID' AND billing_disposition='pending'" | Out-Null
    $WindowStart = (Get-Date).ToUniversalTime().AddMinutes(-20).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    $WindowEnd = (Get-Date).ToUniversalTime().AddMinutes(-19).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    Invoke-Db "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$UserID','$AppID','$UsageCode',100,'unit','$WindowStart','$WindowEnd','$($priceVersion.id)','billing-suspend/$Marker')" | Out-Null
    Wait-Db "SELECT status || '|' || coalesce(suspension_reason,'') FROM user_apps WHERE id='$AppID'" 'suspended|billing_insufficient'
    Wait-Db "SELECT count(*) FROM app_routes WHERE user_app_id='$AppID'" '0'
    Invoke-Db "UPDATE usage_aggregates SET sealed_at=now(),billing_disposition='waived_legacy' WHERE user_id='$UserID' AND billing_disposition='pending' AND NOT (usage_code='$UsageCode' AND window_start='$WindowStart' AND window_end='$WindowEnd')" | Out-Null

    $UnpricedStart = (Get-Date).ToUniversalTime().AddMinutes(-18).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    $UnpricedEnd = (Get-Date).ToUniversalTime().AddMinutes(-17).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    Invoke-Db "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$UserID','$AppID','$UnpricedCode',9,'unit','$UnpricedStart','$UnpricedEnd',NULL,'billing-unpriced/$Marker')" | Out-Null
    Wait-Db "SELECT count(*) FROM usage_aggregates WHERE user_id='$UserID' AND user_app_id='$AppID' AND usage_code='$UnpricedCode' AND window_start='$UnpricedStart' AND billing_disposition='unpriced' AND sealed_at IS NOT NULL" '1'
    if ((Invoke-Db "SELECT count(*) FROM usage_charges WHERE user_id='$UserID' AND user_app_id='$AppID' AND usage_code='$UnpricedCode'") -ne '0') { throw 'unpriced usage was charged' }
    Wait-Db "SELECT balance_cents FROM wallets WHERE user_id='$UserID'" '99'

    $topupCents = [int](Invoke-Db "SELECT greatest(0,amount_cents-balance_cents) FROM usage_billing_attempts WHERE user_id='$UserID' AND user_app_id='$AppID' AND usage_code='$UsageCode' AND window_start='$WindowStart' ORDER BY created_at DESC LIMIT 1")
    if ($topupCents -ne 1) { throw "billing attempt required $topupCents cents instead of 1" }
    $topupOrder = Invoke-RestMethod -Method Post -Uri "$Api/payments/orders" -Headers $UserHeaders -ContentType 'application/json' -Body (@{ amountCents = $topupCents; provider = 'manual'; idempotencyKey = "billing-topup/$Marker" } | ConvertTo-Json -Compress)
    Invoke-RestMethod -Method Post -Uri "$Api/admin/payments/orders/$($topupOrder.orderId)/mark-paid" -Headers $AdminHeaders -ContentType 'application/json' -Body '{}' | Out-Null

    Wait-Db "SELECT balance_cents FROM wallets WHERE user_id='$UserID'" '0' 120
    Wait-Db "SELECT status FROM user_apps WHERE id='$AppID'" 'running' 180
    Wait-Db "SELECT count(*) FROM app_routes WHERE user_app_id='$AppID'" '1' 180
    $chargeCount = Invoke-Db "SELECT count(*) FROM usage_charges WHERE user_id='$UserID' AND user_app_id='$AppID' AND usage_code='$UsageCode' AND window_start='$WindowStart' AND amount_cents=100"
    $ledgerCount = Invoke-Db "SELECT count(*) FROM wallet_ledger_entries l JOIN usage_charges c ON c.wallet_ledger_entry_id=l.id WHERE c.user_id='$UserID' AND c.user_app_id='$AppID' AND c.usage_code='$UsageCode' AND c.window_start='$WindowStart' AND l.business_type='usage'"
    $billItemCount = Invoke-Db "SELECT count(*) FROM bill_items i JOIN usage_charges c ON c.id=i.usage_charge_id WHERE c.user_id='$UserID' AND c.user_app_id='$AppID' AND c.usage_code='$UsageCode' AND c.window_start='$WindowStart' AND i.amount_cents=100"
    $billMismatches = Invoke-Db "SELECT count(*) FROM bills b WHERE b.user_id='$UserID' AND b.total_cents<>(SELECT coalesce(sum(i.amount_cents),0) FROM bill_items i WHERE i.bill_id=b.id)"
    $notificationKinds = Invoke-Db "SELECT string_agg(kind,',' ORDER BY kind) FROM user_notifications WHERE user_id='$UserID' AND (event_key IN (SELECT 'low-balance/'||id::text FROM usage_aggregates WHERE usage_code='$UsageCode' AND window_start='$LowStart') OR event_key IN (SELECT 'billing-suspended/'||id::text FROM usage_aggregates WHERE usage_code='$UsageCode' AND window_start='$WindowStart') OR event_key IN (SELECT 'billing-recovered/'||id::text FROM deployment_jobs WHERE user_app_id='$AppID' AND operation='billing_recovery' ORDER BY created_at DESC LIMIT 1))"
    if ($chargeCount -ne '1' -or $ledgerCount -ne '1' -or $billItemCount -ne '1' -or $billMismatches -ne '0' -or $notificationKinds -ne 'billing_recovered,billing_suspended,low_balance') { throw "billing invariants failed: charge=$chargeCount ledger=$ledgerCount bill=$billItemCount mismatch=$billMismatches notices=$notificationKinds" }
    $usageView = Invoke-RestMethod -Method Get -Uri "$Api/billing/usage" -Headers $UserHeaders
    $unpricedUsage = $usageView.usage | Where-Object { $_.usageCode -eq $UnpricedCode -and $_.billingDisposition -eq 'unpriced' } | Select-Object -First 1
    if ($null -eq $unpricedUsage -or $null -ne $unpricedUsage.amountCents) { throw 'unpriced usage API state is invalid' }

    Write-Host 'Billing warning, unpriced sealing, suspension, route removal, idempotent charge, statement snapshot, notification and automatic recovery verification passed'
} finally {
    if ($OverrideID) {
        try { Invoke-RestMethod -Method Delete -Uri "$Api/admin/pricing/overrides/$OverrideID" -Headers $AdminHeaders | Out-Null } catch { }
        Invoke-DbQuiet "DELETE FROM pricing_overrides WHERE id='$OverrideID'"
    }
    if ($AppID) {
        Invoke-DbQuiet "UPDATE deployment_jobs SET state='failed',last_error=coalesce(last_error,'billing verification cleanup'),updated_at=now() WHERE user_app_id='$AppID' AND state NOT IN ('succeeded','failed'); UPDATE usage_aggregates SET sealed_at=coalesce(sealed_at,now()),billing_disposition=CASE WHEN billing_disposition='pending' THEN 'waived_legacy' ELSE billing_disposition END WHERE user_id='$UserID'; DELETE FROM app_routes WHERE user_app_id='$AppID'; UPDATE user_apps SET status='suspended',suspension_reason='billing_insufficient' WHERE id='$AppID'"
        $containers = @(& docker ps -a --filter "label=cloudmeter.owner=$RuntimeOwner" --filter "label=cloudmeter.app_id=$AppID" --format '{{.Names}}')
        foreach ($runtimeContainer in $containers) { try { & docker rm -f $runtimeContainer *> $null } catch { } }
    }
    if ($UserID) { Invoke-DbQuiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE user_id='$UserID'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$UserID'" }
    Invoke-DbQuiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($AdminSession.ID)'"
    if ($PlanID) { Invoke-DbQuiet "UPDATE plans SET purchase_enabled=false WHERE id='$PlanID'" }
    if ($ProductID) { Invoke-DbQuiet "UPDATE app_products SET status='retired' WHERE id='$ProductID'" }
    Start-Sleep -Seconds 3
    Remove-TestNetwork $Network $RuntimeOwner
}
