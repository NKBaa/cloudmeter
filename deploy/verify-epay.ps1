$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
$Compose = @("docker", "compose")
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @("--project-name", $env:COMPOSE_PROJECT_NAME) }
$Compose += @("--env-file", ".env", "-f", "deploy/compose.yaml")
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$baseURL = "http://127.0.0.1:$port/api"
function DB([string]$query) {
    $result = & $Compose[0] $Compose[1..($Compose.Length-1)] exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc $query
    if ($LASTEXITCODE -ne 0) { throw "database query failed" }
    return (($result | Select-Object -First 1) -as [string]).Trim()
}
function New-Session([string]$userID) {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $token = (($bytes | ForEach-Object { $_.ToString('x2') }) -join '')
    $id = DB "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$userID',digest('$token','sha256'),now()+interval '15 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}
function EPay-Sign([hashtable]$values, [string]$secret) {
    $parts = $values.Keys | Where-Object { $_ -notin @("sign", "sign_type") -and [string]$values[$_] -ne "" } | Sort-Object | ForEach-Object { "$_=$($values[$_])" }
    $md5 = [Security.Cryptography.MD5]::Create()
    try { return (($md5.ComputeHash([Text.Encoding]::UTF8.GetBytes(($parts -join "&") + $secret)) | ForEach-Object { $_.ToString('x2') }) -join '') } finally { $md5.Dispose() }
}
function Post-FormStatus([hashtable]$body) {
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/payments/epay/callback" -Body $body -ContentType "application/x-www-form-urlencoded"
        return [int]$response.StatusCode
    } catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        throw
    }
}
$adminID = DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
$userID = DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='user' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID -or -not $userID) { throw "an active super administrator and ordinary user are required" }
$saved = DB "SELECT enabled::text || chr(9) || merchant_id || chr(9) || endpoint || chr(9) || secret FROM payment_provider_configs WHERE provider='epay'"
    $savedParts = @($saved -split [char]9, 4) + @('', '', '', '')
$admin = New-Session $adminID
$user = New-Session $userID
$merchant = "verify-merchant"
$secret = "verify-secret"
$adminHeaders = @{ Authorization = "Bearer $($admin.Token)" }
$userHeaders = @{ Authorization = "Bearer $($user.Token)" }
try {
    $initialBalance = [int64](DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'")
    $settings = @{ enabled = $true; merchantId = $merchant; endpoint = "https://pay.example.test/submit"; secret = $secret } | ConvertTo-Json -Compress
    Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/payments/epay" -Headers $adminHeaders -ContentType "application/json" -Body $settings | Out-Null
    $key = "verify-epay-$([guid]::NewGuid().ToString('N'))"
    $created = Invoke-RestMethod -Method Post -Uri "$baseURL/payments/orders" -Headers $userHeaders -ContentType "application/json" -Body (@{ amountCents = 1; provider = "epay"; idempotencyKey = $key } | ConvertTo-Json -Compress)
    if (-not $created.orderId -or -not $created.checkoutUrl) { throw "EPay checkout URL was not generated" }
    $checkout = [Uri]$created.checkoutUrl
    if ($checkout.Host -ne "pay.example.test" -or -not $checkout.Query.Contains("sign=")) { throw "EPay checkout URL is invalid" }
    $notify = "http://127.0.0.1:$port/api/payments/epay/callback"
    $values = @{ pid = $merchant; type = "alipay"; out_trade_no = $created.orderId; notify_url = $notify; return_url = "http://127.0.0.1:$port/console"; name = "CloudMeter verification"; money = "0.01"; trade_no = "verify-trade-$([guid]::NewGuid().ToString('N'))"; trade_status = "TRADE_SUCCESS" }
    $invalid = $values.Clone(); $invalid.sign_type = "MD5"; $invalid.sign = "invalid"
    if ((Post-FormStatus $invalid) -ne 403) { throw "invalid EPay signature was accepted" }
    $mismatch = $values.Clone(); $mismatch.money = "0.02"; $mismatch.sign_type = "MD5"; $mismatch.sign = EPay-Sign $mismatch $secret
    if ((Post-FormStatus $mismatch) -ne 400) { throw "EPay amount mismatch was not rejected" }
    $valid = $values.Clone(); $valid.sign_type = "MD5"; $valid.sign = EPay-Sign $valid $secret
    $accepted = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/payments/epay/callback" -Body $valid -ContentType "application/x-www-form-urlencoded"
    if ($accepted.Content.Trim() -ne 'success') { throw "valid EPay callback was not accepted" }
    $replay = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$baseURL/payments/epay/callback" -Body $valid -ContentType "application/x-www-form-urlencoded"
    if ($replay.Content.Trim() -ne 'success') { throw "EPay callback replay was not idempotent" }
    try {
        Invoke-RestMethod -Method Post -Uri "$baseURL/admin/payments/orders/$($created.orderId)/refund" -Headers $adminHeaders -ContentType "application/json" -Body "{`"reason`":`"EPay provider refund guard verification`"}" | Out-Null
        throw "EPay refund unexpectedly succeeded without a provider operation"
    } catch {
        if (-not $_.Exception.Response -or [int]$_.Exception.Response.StatusCode -ne 503) { throw }
    }
    $orderState = DB "SELECT status FROM payment_orders WHERE id='$($created.orderId)'"
    $finalBalance = [int64](DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'")
    $refundCount = DB "SELECT count(*) FROM refunds WHERE order_id='$($created.orderId)'"
    $rejectionAudit = DB "SELECT count(*) FROM audit_logs WHERE action='payment.refund.rejected' AND resource_type='payment_order' AND resource_id='$($created.orderId)' AND metadata->>'cause'='provider_refund_unconfigured'"
    if ($orderState -ne 'paid' -or $finalBalance -ne $initialBalance + 1 -or $refundCount -ne '0' -or $rejectionAudit -ne '1') {
        throw "EPay refund guard changed financial state or missed its audit record"
    }
    Write-Host "EPay checkout, signature rejection, amount validation, callback idempotency and provider-refund guard passed"
} finally {
    $enabled = if ($savedParts[0] -eq "true") { "true" } else { "false" }
    $merchantSaved = ([string]$savedParts[1]).Replace("'", "''")
    $endpointSaved = ([string]$savedParts[2]).Replace("'", "''")
    $secretSaved = ([string]$savedParts[3]).Replace("'", "''")
    DB "UPDATE payment_provider_configs SET enabled=$enabled,merchant_id='$merchantSaved',endpoint='$endpointSaved',secret='$secretSaved',updated_at=now() WHERE provider='epay'" | Out-Null
    DB "UPDATE sessions SET revoked_at=now() WHERE id IN ('$($admin.ID)','$($user.ID)')" | Out-Null
}
