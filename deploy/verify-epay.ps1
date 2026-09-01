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
$saved = DB "SELECT encode(convert_to(enabled::text,'UTF8'),'hex') || ':' || encode(convert_to(merchant_id,'UTF8'),'hex') || ':' || encode(convert_to(endpoint,'UTF8'),'hex') || ':' || encode(convert_to(secret,'UTF8'),'hex') || ':' || encode(convert_to(payment_type,'UTF8'),'hex') || ':' || encode(convert_to(payment_methods::text,'UTF8'),'hex') || ':' || encode(convert_to(amount_options::text,'UTF8'),'hex') FROM payment_provider_configs WHERE provider='epay'"
$savedParts = @($saved -split ':', 7) + @('', '', '', '', '', '', '')
$admin = New-Session $adminID
$user = New-Session $userID
$merchant = "verify-merchant"
$secret = "verify-secret"
$adminHeaders = @{ Authorization = "Bearer $($admin.Token)" }
$userHeaders = @{ Authorization = "Bearer $($user.Token)" }
$createdOrderID = ""
try {
    $initialBalance = [int64](DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'")
    $settings = @{ enabled = $true; merchantId = $merchant; endpoint = "https://pay.example.test/submit"; secret = $secret; paymentType = "alipay"; paymentMethods = @(@{ name = "Verification"; type = "alipay"; minAmountCents = 100; enabled = $true }); amountOptions = @(1) } | ConvertTo-Json -Compress -Depth 4
    Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/payments/epay" -Headers $adminHeaders -ContentType "application/json" -Body $settings | Out-Null
    $key = "verify-epay-$([guid]::NewGuid().ToString('N'))"
    $created = Invoke-RestMethod -Method Post -Uri "$baseURL/payments/orders" -Headers $userHeaders -ContentType "application/json" -Body (@{ amountCents = 100; provider = "epay"; paymentType = "alipay"; idempotencyKey = $key } | ConvertTo-Json -Compress)
    $createdOrderID = [string]$created.orderId
    if (-not $created.orderId -or -not $created.checkoutUrl) { throw "EPay checkout URL was not generated" }
    $checkout = [Uri]$created.checkoutUrl
    if ($checkout.Host -ne "pay.example.test" -or -not $checkout.Query.Contains("sign=")) { throw "EPay checkout URL is invalid" }
    $notify = "http://127.0.0.1:$port/api/payments/epay/callback"
    $values = @{ pid = $merchant; type = "alipay"; out_trade_no = $created.orderId; notify_url = $notify; return_url = "http://127.0.0.1:$port/console"; name = "CloudMeter verification"; money = "1.00"; trade_no = "verify-trade-$([guid]::NewGuid().ToString('N'))"; trade_status = "TRADE_SUCCESS" }
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
    if ($orderState -ne 'paid' -or $finalBalance -ne $initialBalance + 100 -or $refundCount -ne '0' -or $rejectionAudit -ne '1') {
        throw "EPay refund guard changed financial state or missed its audit record"
    }
    Write-Host "EPay checkout, signature rejection, amount validation, callback idempotency and provider-refund guard passed"
} finally {
    if ($createdOrderID) {
        DB "WITH original AS (SELECT le.id,le.wallet_id,le.amount_cents FROM wallet_ledger_entries le WHERE le.business_type='topup' AND le.business_ref='$createdOrderID'), locked AS (SELECT w.id,w.balance_cents FROM wallets w JOIN original o ON o.wallet_id=w.id FOR UPDATE), reversed AS (INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,reversal_of,metadata) SELECT o.wallet_id,'reversal','verify-epay-reversal/' || '$createdOrderID',-o.amount_cents,l.balance_cents-o.amount_cents,o.id,jsonb_build_object('reason','EPay simulation cleanup','order_id','$createdOrderID') FROM original o JOIN locked l ON l.id=o.wallet_id WHERE NOT EXISTS (SELECT 1 FROM wallet_ledger_entries r WHERE r.reversal_of=o.id) RETURNING wallet_id,amount_cents) UPDATE wallets w SET balance_cents=w.balance_cents+r.amount_cents,version=w.version+1 FROM reversed r WHERE w.id=r.wallet_id" | Out-Null
    }
    $enabledSaved, $merchantSaved, $endpointSaved, $secretSaved, $typeSaved, $methodsSaved, $amountsSaved = $savedParts[0..6]
    DB "UPDATE payment_provider_configs SET enabled=convert_from(decode('$enabledSaved','hex'),'UTF8')::boolean,merchant_id=convert_from(decode('$merchantSaved','hex'),'UTF8'),endpoint=convert_from(decode('$endpointSaved','hex'),'UTF8'),secret=convert_from(decode('$secretSaved','hex'),'UTF8'),payment_type=coalesce(nullif(convert_from(decode('$typeSaved','hex'),'UTF8'),''),'alipay'),payment_methods=convert_from(decode('$methodsSaved','hex'),'UTF8')::jsonb,amount_options=convert_from(decode('$amountsSaved','hex'),'UTF8')::jsonb,updated_at=now() WHERE provider='epay'" | Out-Null
    DB "UPDATE sessions SET revoked_at=now() WHERE id IN ('$($admin.ID)','$($user.ID)')" | Out-Null
}
