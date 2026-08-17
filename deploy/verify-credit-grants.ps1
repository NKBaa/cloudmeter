$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$Compose = @('docker', 'compose')
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @('--project-name', $env:COMPOSE_PROJECT_NAME) }
$Compose += @('--env-file', '.env', '-f', 'deploy/compose.yaml')
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$Port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$Api = "http://127.0.0.1:$Port/api"

function Invoke-Db([string]$Query) {
    $value = & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc $Query
    if ($LASTEXITCODE -ne 0) { throw 'database query failed' }
    return (($value | Select-Object -First 1) -as [string]).Trim()
}

function New-Session([string]$UserID) {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $token = ([Convert]::ToBase64String($bytes)).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    $id = Invoke-Db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$UserID',digest('$token','sha256'),now()+interval '20 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}

function Wait-Db([string]$Query, [string]$Expected, [int]$Seconds = 90) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $value = ''
    do {
        $value = Invoke-Db $Query
        if ($value -eq $Expected) { return }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for '$Expected'; last value was '$value'"
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

$AdminID = Invoke-Db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $AdminID) { throw 'an active super administrator is required' }
$AdminSession = New-Session $AdminID
$AdminHeaders = @{ Authorization = "Bearer $($AdminSession.Token)" }
$Marker = [guid]::NewGuid().ToString('N').Substring(0, 12)
$Password = "Credit-$Marker-Password!"
$UserID = ''
$UserSession = $null
$OverrideID = ''
$UsageCode = 'verify.credit.units'

try {
    $created = Invoke-RestMethod -Method Post -Uri "$Api/admin/users" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{
        email = "credit-$Marker@example.invalid"
        password = $Password
        displayName = 'Credit verification'
        role = 'user'
    } | ConvertTo-Json -Compress)
    $UserID = $created.id
    $UserSession = New-Session $UserID
    $UserHeaders = @{ Authorization = "Bearer $($UserSession.Token)" }

    $catalog = Invoke-RestMethod -Method Get -Uri "$Api/admin/pricing" -Headers $AdminHeaders
    $item = $catalog.items | Where-Object { $_.code -eq $UsageCode -and $_.unit -eq 'unit' } | Select-Object -First 1
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
        $priceVersion = Invoke-RestMethod -Method Post -Uri "$Api/admin/pricing/items/$($item.id)/versions" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{
            unitPriceMicros = 1000000
            precisionScale = 6
            roundingMode = 'half_up'
            minimumQuantity = '0'
            freeQuantity = '0'
            effectiveAt = $now.AddDays(-1).ToString('o')
        } | ConvertTo-Json -Compress)
    }
    $override = Invoke-RestMethod -Method Put -Uri "$Api/admin/pricing/overrides" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{
        pricingItemId = $item.id
        pricingVersionId = $priceVersion.id
        scope = 'user'
        scopeId = $UserID
    } | ConvertTo-Json -Compress)
    $OverrideID = $override.id

    $ExpiredRef = "credit-expired/$Marker"
    $RecoveryRef = "credit-recovery/$Marker"
    $MixedRef = "credit-mixed/$Marker"
    $RecoveryStart = (Get-Date).ToUniversalTime().AddMinutes(-14).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    $RecoveryEnd = (Get-Date).ToUniversalTime().AddMinutes(-13).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    $MixedStart = (Get-Date).ToUniversalTime().AddMinutes(-12).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    $MixedEnd = (Get-Date).ToUniversalTime().AddMinutes(-11).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')

    Invoke-Db "INSERT INTO credit_grants(user_id,amount_cents,remaining_cents,business_ref,note,expires_at,created_by) VALUES('$UserID',9,9,'$ExpiredRef','Expired validation fixture',now()-interval '1 minute','$AdminID'); INSERT INTO usage_events(user_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$UserID','$UsageCode',7,'unit','$RecoveryStart','$RecoveryEnd','$($priceVersion.id)','credit-recovery/$Marker')" | Out-Null
    Wait-Db "SELECT count(*) FROM usage_billing_attempts WHERE user_id='$UserID' AND usage_code='$UsageCode' AND window_start='$RecoveryStart' AND status='insufficient_funds' AND credit_balance_cents=0" '1'

    $grantBody = @{
        amountCents = 7
        businessRef = $RecoveryRef
        note = 'Recovery credit'
        expiresAt = (Get-Date).ToUniversalTime().AddHours(2).ToString('o')
    } | ConvertTo-Json -Compress
    $grant = Invoke-RestMethod -Method Post -Uri "$Api/admin/users/$UserID/credits" -Headers $AdminHeaders -ContentType 'application/json' -Body $grantBody
    $replay = Invoke-RestMethod -Method Post -Uri "$Api/admin/users/$UserID/credits" -Headers $AdminHeaders -ContentType 'application/json' -Body $grantBody
    if (-not $replay.idempotent) { throw 'credit replay is not idempotent' }
    $conflictBody = @{ amountCents = 8; businessRef = $RecoveryRef; note = 'Recovery credit'; expiresAt = $null } | ConvertTo-Json -Compress
    if ((Get-Status 'POST' "$Api/admin/users/$UserID/credits" $AdminHeaders $conflictBody) -ne 409) { throw 'credit idempotency conflict was not rejected' }
    Wait-Db "SELECT count(*) FROM credit_consumptions c JOIN credit_grants g ON g.id=c.credit_grant_id WHERE g.business_ref='$RecoveryRef'" '1'

    $order = Invoke-RestMethod -Method Post -Uri "$Api/payments/orders" -Headers $UserHeaders -ContentType 'application/json' -Body (@{ amountCents = 3; provider = 'manual'; idempotencyKey = "credit-mixed-topup/$Marker" } | ConvertTo-Json -Compress)
    Invoke-RestMethod -Method Post -Uri "$Api/admin/payments/orders/$($order.orderId)/mark-paid" -Headers $AdminHeaders -ContentType 'application/json' -Body '{}' | Out-Null
    Invoke-RestMethod -Method Post -Uri "$Api/admin/users/$UserID/credits" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ amountCents = 2; businessRef = $MixedRef; note = 'Mixed credit' } | ConvertTo-Json -Compress) | Out-Null
    Invoke-Db "INSERT INTO usage_events(user_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$UserID','$UsageCode',5,'unit','$MixedStart','$MixedEnd','$($priceVersion.id)','credit-mixed/$Marker')" | Out-Null
    Wait-Db "SELECT count(*) FROM usage_charges WHERE user_id='$UserID' AND usage_code='$UsageCode' AND window_start='$MixedStart'" '1'

    $result = Invoke-Db "SELECT (SELECT remaining_cents FROM credit_grants WHERE business_ref='$ExpiredRef')||'|'||(SELECT remaining_cents FROM credit_grants WHERE business_ref='$RecoveryRef')||'|'||(SELECT remaining_cents FROM credit_grants WHERE business_ref='$MixedRef')||'|'||(SELECT balance_cents FROM wallets WHERE user_id='$UserID')||'|'||(SELECT c.amount_cents FROM credit_consumptions c JOIN credit_grants g ON g.id=c.credit_grant_id WHERE g.business_ref='$MixedRef')||'|'||(SELECT amount_cents FROM usage_charges WHERE user_id='$UserID' AND usage_code='$UsageCode' AND window_start='$MixedStart')"
    if ($result -ne '9|0|0|0|2|5') { throw "credit settlement mismatch: $result" }
    $view = Invoke-RestMethod -Method Get -Uri "$Api/billing/credits" -Headers $UserHeaders
    if (-not ($view.grants.id -contains $grant.id)) { throw 'owner cannot see credit grant' }

    Write-Host 'Credit expiration, idempotency, grant-only retry, priority consumption, mixed wallet settlement and owner visibility verification passed'
} finally {
    if ($OverrideID) {
        try { Invoke-RestMethod -Method Delete -Uri "$Api/admin/pricing/overrides/$OverrideID" -Headers $AdminHeaders | Out-Null } catch { }
        try { Invoke-Db "DELETE FROM pricing_overrides WHERE id='$OverrideID'" | Out-Null } catch { }
    }
    if ($UserID) {
        try { Invoke-Db "UPDATE usage_aggregates SET sealed_at=coalesce(sealed_at,now()),billing_disposition=CASE WHEN billing_disposition='pending' THEN 'waived_legacy' ELSE billing_disposition END WHERE user_id='$UserID'; UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE user_id='$UserID'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$UserID'" | Out-Null } catch { }
    }
    try { Invoke-Db "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($AdminSession.ID)'" | Out-Null } catch { }
}
