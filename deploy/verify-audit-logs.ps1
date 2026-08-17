$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$Compose = @('docker', 'compose')
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @('--project-name', $env:COMPOSE_PROJECT_NAME) }
$Compose += @('--env-file', '.env', '-f', 'deploy/compose.yaml')
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$Port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$Api = "http://127.0.0.1:$Port/api"
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
        & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -c "BEGIN; $Query; ROLLBACK;" 2>$null | Out-Null
        $exitCode = $LASTEXITCODE
    } catch {
        $exitCode = 1
    }
    if ($exitCode -eq 0) { throw "database accepted forbidden audit mutation: $Description" }
}

function New-Session([string]$UserID) {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $token = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    $escaped = $token.Replace("'", "''")
    $id = DB "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$UserID',digest('$escaped','sha256'),now()+interval '15 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}

function Get-Status([string]$Uri, [hashtable]$Headers = $null) {
    try {
        $params = @{ UseBasicParsing = $true; Method = 'GET'; Uri = $Uri }
        if ($Headers) { $params.Headers = $Headers }
        return [int](Invoke-WebRequest @params).StatusCode
    } catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        throw
    }
}

$migrationState = DB "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations"
if ($migrationState -ne "$LatestMigration|clean") {
    throw "migration $LatestMigration must be applied before audit verification; current state is $migrationState"
}
$adminID = DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
$user = DB "SELECT u.id::text || chr(9) || u.email FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='user' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID -or -not $user) { throw 'an active super administrator and ordinary user are required' }
$userParts = $user -split [char]9, 2
$userID = $userParts[0]
$userEmail = $userParts[1]
$adminSession = New-Session $adminID
$userSession = New-Session $userID
try {
    $adminHeaders = @{ Authorization = "Bearer $($adminSession.Token)" }
    $userHeaders = @{ Authorization = "Bearer $($userSession.Token)" }
    if ((Get-Status "$Api/admin/audit-logs") -ne 401) { throw 'unauthenticated audit access did not return 401' }
    if ((Get-Status "$Api/admin/audit-logs" $userHeaders) -ne 403) { throw 'ordinary user audit access did not return 403' }

    $marker = [guid]::NewGuid().ToString('N')
    $actionPrefix = "verification.audit.$marker"
    $firstID = DB "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES('$adminID','$userID','$actionPrefix.first','audit_verification','$marker-first','verify-audit/$marker',jsonb_build_object('sequence',1,'marker','$marker')) RETURNING id"
    $secondID = DB "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES('$adminID','$userID','$actionPrefix.second','audit_verification','$marker-second','verify-audit/$marker',jsonb_build_object('sequence',2,'marker','$marker')) RETURNING id"

    $encodedAction = [Uri]::EscapeDataString($actionPrefix)
    $encodedIdentity = [Uri]::EscapeDataString($userEmail)
    $firstPage = Invoke-RestMethod -Method Get -Uri "$Api/admin/audit-logs?limit=1&action=$encodedAction&identity=$encodedIdentity" -Headers $adminHeaders
    if (@($firstPage.logs).Count -ne 1 -or [string]$firstPage.logs[0].id -ne $secondID -or -not $firstPage.nextBefore) {
        throw 'audit filter or first cursor page is incorrect'
    }
    $secondPage = Invoke-RestMethod -Method Get -Uri "$Api/admin/audit-logs?limit=1&action=$encodedAction&identity=$encodedIdentity&before=$($firstPage.nextBefore)" -Headers $adminHeaders
    if (@($secondPage.logs).Count -ne 1 -or [string]$secondPage.logs[0].id -ne $firstID -or $secondPage.nextBefore) {
        throw 'audit cursor did not return the earlier record exactly once'
    }

    Assert-DBRejected "UPDATE audit_logs SET action=action || '.changed' WHERE id=$firstID" 'UPDATE'
    Assert-DBRejected "DELETE FROM audit_logs WHERE id=$firstID" 'DELETE'
    Assert-DBRejected 'TRUNCATE audit_logs' 'TRUNCATE'
    if ((DB "SELECT count(*) FROM audit_logs WHERE id IN ($firstID,$secondID) AND metadata->>'marker'='$marker'") -ne '2') {
        throw 'audit evidence changed during immutability verification'
    }

    Write-Host "Audit authorization, filters, cursor pagination and append-only database protection passed ($firstID, $secondID)"
} finally {
    try { DB "UPDATE sessions SET revoked_at=now() WHERE id IN ('$($adminSession.ID)','$($userSession.ID)') RETURNING id" | Out-Null } catch { Write-Warning 'temporary audit verification sessions could not be revoked' }
}
