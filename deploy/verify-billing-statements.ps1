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

function Get-Status([string]$Method, [string]$Uri, [hashtable]$Headers = $null) {
    try {
        $params = @{ UseBasicParsing = $true; Method = $Method; Uri = $Uri }
        if ($Headers) { $params.Headers = $Headers }
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
$Password = "Statement-$Marker-Password!"
$OwnerID = ''
$OtherID = ''
$OwnerSession = $null
$OtherSession = $null
$OverrideID = ''
$UsageCode = 'verify.statement.units'

try {
    $owner = Invoke-RestMethod -Method Post -Uri "$Api/admin/users" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ email = "statement-owner-$Marker@example.invalid"; password = $Password; displayName = 'Statement owner'; role = 'user' } | ConvertTo-Json -Compress)
    $other = Invoke-RestMethod -Method Post -Uri "$Api/admin/users" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ email = "statement-other-$Marker@example.invalid"; password = $Password; displayName = 'Statement isolation'; role = 'user' } | ConvertTo-Json -Compress)
    $OwnerID = $owner.id
    $OtherID = $other.id
    $OwnerSession = New-Session $OwnerID
    $OtherSession = New-Session $OtherID
    $OwnerHeaders = @{ Authorization = "Bearer $($OwnerSession.Token)" }
    $OtherHeaders = @{ Authorization = "Bearer $($OtherSession.Token)" }

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
        $priceVersion = Invoke-RestMethod -Method Post -Uri "$Api/admin/pricing/items/$($item.id)/versions" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ unitPriceMicros = 1000000; precisionScale = 6; roundingMode = 'half_up'; minimumQuantity = '0'; freeQuantity = '0'; effectiveAt = $now.AddDays(-1).ToString('o') } | ConvertTo-Json -Compress)
    }
    $override = Invoke-RestMethod -Method Put -Uri "$Api/admin/pricing/overrides" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ pricingItemId = $item.id; pricingVersionId = $priceVersion.id; scope = 'user'; scopeId = $OwnerID } | ConvertTo-Json -Compress)
    $OverrideID = $override.id

    Invoke-RestMethod -Method Post -Uri "$Api/admin/users/$OwnerID/wallet/adjust" -Headers $AdminHeaders -ContentType 'application/json' -Body (@{ amountCents = 25; businessRef = "statement-seed/$Marker"; note = 'Statement verification' } | ConvertTo-Json -Compress) | Out-Null
    $windowStart = (Get-Date).ToUniversalTime().AddMinutes(-10).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    $windowEnd = (Get-Date).ToUniversalTime().AddMinutes(-9).ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
    Invoke-Db "INSERT INTO usage_events(user_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$OwnerID','$UsageCode',7,'unit','$windowStart','$windowEnd','$($priceVersion.id)','statement/$Marker')" | Out-Null
    Wait-Db "SELECT count(*) FROM usage_charges WHERE user_id='$OwnerID' AND usage_code='$UsageCode' AND window_start='$windowStart' AND amount_cents=7" '1'
    $BillID = Invoke-Db "SELECT b.id FROM bills b JOIN bill_items i ON i.bill_id=b.id WHERE b.user_id='$OwnerID' AND i.usage_code='$UsageCode' ORDER BY b.created_at DESC LIMIT 1"
    if (-not $BillID) { throw 'dynamic billing statement was not created' }

    if ((Get-Status 'GET' "$Api/billing/bills") -ne 401) { throw 'unauthenticated statement request was not rejected' }
    $list = Invoke-RestMethod -Method Get -Uri "$Api/billing/bills" -Headers $OwnerHeaders
    if (-not ($list.bills.id -contains $BillID)) { throw 'owner statement is absent from list' }
    $detail = Invoke-RestMethod -Method Get -Uri "$Api/billing/bills/$BillID" -Headers $OwnerHeaders
    $sum = ($detail.items | Measure-Object amountCents -Sum).Sum
    if ([int64]$sum -ne [int64]$detail.bill.totalCents -or [int64]$detail.bill.totalCents -ne 7) { throw 'statement total does not equal the generated item total' }
    foreach ($path in @("/billing/bills/$BillID", "/billing/bills/$BillID/export")) {
        if ((Get-Status 'GET' "$Api$path" $OtherHeaders) -ne 404) { throw 'cross-account statement access was not hidden' }
    }
    $csv = Invoke-WebRequest -UseBasicParsing -Method Get -Uri "$Api/billing/bills/$BillID/export" -Headers $OwnerHeaders
    $feeItemHeader = -join @([char]0x8D39, [char]0x7528, [char]0x9879)
    if ($csv.Headers.'Content-Type' -notlike 'text/csv*' -or $csv.Content -notmatch [regex]::Escape($feeItemHeader) -or $csv.Content -notmatch [regex]::Escape($UsageCode)) { throw 'CSV statement is invalid' }

    Write-Host "Billing statement authentication, dynamic generation, isolation, totals and CSV verification passed ($($detail.items.Count) item(s))"
} finally {
    if ($OverrideID) {
        try { Invoke-RestMethod -Method Delete -Uri "$Api/admin/pricing/overrides/$OverrideID" -Headers $AdminHeaders | Out-Null } catch { }
        try { Invoke-Db "DELETE FROM pricing_overrides WHERE id='$OverrideID'" | Out-Null } catch { }
    }
    foreach ($id in @($OwnerID, $OtherID) | Where-Object { $_ }) {
        try { Invoke-Db "UPDATE usage_aggregates SET sealed_at=coalesce(sealed_at,now()),billing_disposition=CASE WHEN billing_disposition='pending' THEN 'waived_legacy' ELSE billing_disposition END WHERE user_id='$id'; UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE user_id='$id'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$id'" | Out-Null } catch { }
    }
    try { Invoke-Db "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($AdminSession.ID)'" | Out-Null } catch { }
}
