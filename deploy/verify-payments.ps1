$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$Compose = @('docker', 'compose')
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @('--project-name', $env:COMPOSE_PROJECT_NAME) }
$Compose += @('--env-file', '.env', '-f', 'deploy/compose.yaml')
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$Port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$BaseURL = "http://127.0.0.1:$Port/api"
$LatestMigration = (Get-ChildItem (Join-Path $Root 'migrations/*.up.sql') | ForEach-Object {
    if ($_.Name -match '^0*(\d+)_') { [int]$Matches[1] }
} | Sort-Object | Select-Object -Last 1)

function DB([string]$Query) {
    $result = & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc $Query
    if ($LASTEXITCODE -ne 0) { throw 'database query failed' }
    return (($result | Select-Object -First 1) -as [string]).Trim()
}

function Assert-DBRejected([string]$Query, [string]$Description) {
    $exitCode = 1
    try {
        & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -c $Query 2>$null | Out-Null
        $exitCode = $LASTEXITCODE
    } catch {
        $exitCode = 1
    }
    if ($exitCode -eq 0) { throw "database accepted forbidden mutation: $Description" }
}

function New-TemporarySession([string]$UserID) {
    $bytes = New-Object byte[] 32
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $random.GetBytes($bytes) } finally { $random.Dispose() }
    $token = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    $escaped = $token.Replace("'", "''")
    $id = DB "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$UserID',digest('$escaped','sha256'),now()+interval '15 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}

$migrationState = DB "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations"
if ($migrationState -ne "$LatestMigration|clean") {
    throw "migration $LatestMigration must be applied before payment verification; current state is $migrationState"
}

$adminID = DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID) { throw 'an active super administrator is required' }

$adminSession = New-TemporarySession $adminID
$userSession = $null
$userID = ''
try {
    $adminHeaders = @{ Authorization = "Bearer $($adminSession.Token)" }
    $marker = [guid]::NewGuid().ToString('N')
    $accountBody = @{
        email = "payment-verify-$marker@example.invalid"
        password = "Pay-$marker"
        displayName = 'Payment refund verification'
        role = 'user'
    } | ConvertTo-Json -Compress
    $createdUser = Invoke-RestMethod -Method Post -Uri "$BaseURL/admin/users" -Headers $adminHeaders -ContentType 'application/json' -Body $accountBody
    $userID = $createdUser.id
    $userSession = New-TemporarySession $userID
    $userHeaders = @{ Authorization = "Bearer $($userSession.Token)" }

    $initialBalance = [int64](DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'")
    $key = "verify-payment-$marker"
    $body = @{ amountCents = 1; provider = 'manual'; idempotencyKey = $key } | ConvertTo-Json -Compress
    $created = Invoke-RestMethod -Method Post -Uri "$BaseURL/payments/orders" -Headers $userHeaders -ContentType 'application/json' -Body $body
    $replayed = Invoke-RestMethod -Method Post -Uri "$BaseURL/payments/orders" -Headers $userHeaders -ContentType 'application/json' -Body $body
    if ($created.orderId -ne $replayed.orderId -or -not $replayed.idempotent) { throw 'order creation replay was not idempotent' }

    $conflictBody = @{ amountCents = 2; provider = 'manual'; idempotencyKey = $key } | ConvertTo-Json -Compress
    try {
        Invoke-RestMethod -Method Post -Uri "$BaseURL/payments/orders" -Headers $userHeaders -ContentType 'application/json' -Body $conflictBody | Out-Null
        throw 'order idempotency conflict unexpectedly succeeded'
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 409) { throw }
    }

    $paid = Invoke-RestMethod -Method Post -Uri "$BaseURL/admin/payments/orders/$($created.orderId)/mark-paid" -Headers $adminHeaders -ContentType 'application/json' -Body '{}'
    $paidReplay = Invoke-RestMethod -Method Post -Uri "$BaseURL/admin/payments/orders/$($created.orderId)/mark-paid" -Headers $adminHeaders -ContentType 'application/json' -Body '{}'
    if ($paid.balanceCents -ne $initialBalance + 1 -or -not $paidReplay.idempotent) { throw 'manual payment replay changed the wallet incorrectly' }

    $refundBody = @{ reason = "concurrent refund verification $marker" } | ConvertTo-Json -Compress
    $refundURI = "$BaseURL/admin/payments/orders/$($created.orderId)/refund"
    $jobs = 1..2 | ForEach-Object {
        Start-Job -ScriptBlock {
            param($URI, $Token, $Body)
            try {
                $response = Invoke-WebRequest -UseBasicParsing -Method Post -Uri $URI -Headers @{ Authorization = "Bearer $Token" } -ContentType 'application/json' -Body $Body
                [pscustomobject]@{ Status = [int]$response.StatusCode; Body = $response.Content }
            } catch {
                $status = if ($_.Exception.Response) { [int]$_.Exception.Response.StatusCode } else { 0 }
                [pscustomobject]@{ Status = $status; Body = $_.ErrorDetails.Message }
            }
        } -ArgumentList $refundURI, $adminSession.Token, $refundBody
    }
    try {
        $responses = @($jobs | Wait-Job | Receive-Job)
    } finally {
        $jobs | Remove-Job -Force
    }
    if ($responses.Count -ne 2 -or @($responses | Where-Object { $_.Status -ne 200 }).Count) {
        throw "concurrent refunds returned: $($responses.Status -join ',')"
    }
    $refundResults = @($responses | ForEach-Object { $_.Body | ConvertFrom-Json })
    if (@($refundResults.refundId | Sort-Object -Unique).Count -ne 1 -or @($refundResults.ledgerEntryId | Sort-Object -Unique).Count -ne 1) {
        throw 'concurrent refunds returned different immutable records'
    }
    if (@($refundResults | Where-Object { $_.idempotent -eq $false }).Count -ne 1 -or @($refundResults | Where-Object { $_.idempotent -eq $true }).Count -ne 1) {
        throw 'concurrent refunds did not produce exactly one mutation and one replay'
    }
    $refund = $refundResults | Where-Object { $_.idempotent -eq $false } | Select-Object -First 1
    $refundReplay = Invoke-RestMethod -Method Post -Uri $refundURI -Headers $adminHeaders -ContentType 'application/json' -Body $refundBody
    if (-not $refundReplay.idempotent -or $refundReplay.refundId -ne $refund.refundId -or $refundReplay.ledgerEntryId -ne $refund.ledgerEntryId) {
        throw 'refund replay did not return the original refund record'
    }

    $refundList = Invoke-RestMethod -Method Get -Uri "$BaseURL/admin/payments/refunds" -Headers $adminHeaders
    $listedRefund = $refundList.refunds | Where-Object { $_.id -eq $refund.refundId } | Select-Object -First 1
    if (-not $listedRefund -or $listedRefund.status -ne 'succeeded' -or @($listedRefund.events).Count -ne 2) {
        throw 'administrator refund timeline query is incomplete'
    }

    $finalBalance = [int64](DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'")
    $ledgerCount = [int](DB "SELECT count(*) FROM wallet_ledger_entries entry JOIN wallets wallet ON wallet.id=entry.wallet_id WHERE wallet.user_id='$userID' AND entry.business_ref='$($created.orderId)' AND entry.business_type IN ('topup','refund')")
    if ($finalBalance -ne $initialBalance -or $ledgerCount -ne 2) { throw 'wallet or append-only ledger invariant failed' }

    $refundInvariant = DB "SELECT count(*) FROM refunds rf JOIN payment_orders o ON o.id=rf.order_id JOIN wallet_ledger_entries le ON le.id=rf.ledger_entry_id JOIN wallets w ON w.id=le.wallet_id WHERE rf.id='$($refund.refundId)' AND rf.status='succeeded' AND o.status='refunded' AND rf.user_id='$userID' AND w.user_id=rf.user_id AND le.business_type='refund' AND le.business_ref=o.id::text AND le.amount_cents=-o.amount_cents AND rf.ledger_entry_id=$($refund.ledgerEntryId)"
    if ($refundInvariant -ne '1') { throw 'refund snapshot, order and ledger are not aligned' }
    if ((DB "SELECT string_agg(to_status,',' ORDER BY id) FROM refund_events WHERE refund_id='$($refund.refundId)'") -ne 'processing,succeeded') { throw 'refund event timeline is invalid' }
    if ((DB "SELECT count(*) FROM audit_logs WHERE action='payment.refund' AND resource_type='refund' AND resource_id='$($refund.refundId)' AND subject_user_id='$userID' AND metadata->>'order_id'='$($created.orderId)'") -ne '1') { throw 'refund audit record is missing' }

    Assert-DBRejected "UPDATE refunds SET reason=reason || ' changed' WHERE id='$($refund.refundId)'" 'refund identity update'
    Assert-DBRejected "DELETE FROM refunds WHERE id='$($refund.refundId)'" 'refund deletion'
    Assert-DBRejected "UPDATE refund_events SET message=message || ' changed' WHERE refund_id='$($refund.refundId)'" 'refund event update'
    Assert-DBRejected "DELETE FROM refund_events WHERE refund_id='$($refund.refundId)'" 'refund event deletion'
    Assert-DBRejected 'BEGIN; TRUNCATE refund_events; ROLLBACK;' 'refund event truncation'
    Assert-DBRejected 'BEGIN; TRUNCATE refunds CASCADE; ROLLBACK;' 'refund history truncation'
    Assert-DBRejected "UPDATE wallet_ledger_entries SET amount_cents=amount_cents+1 WHERE id=$($refund.ledgerEntryId)" 'wallet ledger update'
    Assert-DBRejected "DELETE FROM wallet_ledger_entries WHERE id=$($refund.ledgerEntryId)" 'wallet ledger deletion'
    Assert-DBRejected 'BEGIN; TRUNCATE wallet_ledger_entries CASCADE; ROLLBACK;' 'wallet ledger truncation'
    Assert-DBRejected "UPDATE payment_orders SET amount_cents=amount_cents+1 WHERE id='$($created.orderId)'" 'refunded order snapshot update'

    Write-Host "Payment/refund verification passed; order $($created.orderId), refund $($refund.refundId), wallet net change 0 cents"
} finally {
    $sessionIDs = @($adminSession.ID)
    if ($userSession) { $sessionIDs += $userSession.ID }
    $quotedSessionIDs = ($sessionIDs | ForEach-Object { "'$_'" }) -join ','
    try { DB "UPDATE sessions SET revoked_at=now() WHERE id IN ($quotedSessionIDs) RETURNING id" | Out-Null } catch { Write-Warning 'temporary sessions could not be revoked' }
    if ($userID) {
        try { DB "UPDATE users SET status='suspended' WHERE id='$userID' RETURNING id" | Out-Null } catch { Write-Warning 'temporary payment verification account could not be suspended' }
    }
}
