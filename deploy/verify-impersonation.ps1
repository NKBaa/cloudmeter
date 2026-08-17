$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
$Compose = @("docker", "compose")
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @("--project-name", $env:COMPOSE_PROJECT_NAME) }
$Compose += @("--env-file", ".env", "-f", "deploy/compose.yaml")
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$port = if ($env:PLATFORM_PORT) { [int]$env:PLATFORM_PORT } else { 8080 }
$baseURL = "http://127.0.0.1:$port/api"

function Invoke-DatabaseScalar([string]$query) {
    $result = & $Compose[0] $Compose[1..($Compose.Length-1)] exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc $query
    if ($LASTEXITCODE -ne 0) { throw "database query failed" }
    return (($result | Select-Object -First 1) -as [string]).Trim()
}

$adminID = Invoke-DatabaseScalar "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
$target = Invoke-DatabaseScalar "SELECT u.id::text || '|' || u.email FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='user' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID -or -not $target) { throw "an active super administrator and ordinary user are required" }
$targetID, $targetEmail = $target -split '\|', 2

$bytes = New-Object byte[] 32
$random = [Security.Cryptography.RandomNumberGenerator]::Create()
try { $random.GetBytes($bytes) } finally { $random.Dispose() }
$adminToken = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
$escapedToken = $adminToken.Replace("'", "''")
$adminSessionID = Invoke-DatabaseScalar "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$adminID',digest('$escapedToken','sha256'),now()+interval '15 minutes') RETURNING id"

try {
    $adminHeaders = @{ Authorization = "Bearer $adminToken" }
    $readSession = Invoke-RestMethod -Method Post -Uri "$baseURL/admin/users/$targetID/impersonation" -Headers $adminHeaders -ContentType "application/json" -Body '{"writeEnabled":false,"confirmation":""}'
    $readHeaders = @{ Authorization = "Bearer $($readSession.token)" }
    $identity = Invoke-RestMethod -Uri "$baseURL/me" -Headers $readHeaders
    if (-not $identity.Impersonating -or -not $identity.ImpersonationReadOnly) { throw "read-only impersonation identity mismatch" }
    try {
        Invoke-RestMethod -Method Post -Uri "$baseURL/payments/orders" -Headers $readHeaders -ContentType "application/json" -Body '{}' | Out-Null
        throw "read-only write unexpectedly succeeded"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 403) { throw }
    }
    Invoke-RestMethod -Method Delete -Uri "$baseURL/impersonation" -Headers $readHeaders | Out-Null
    try {
        Invoke-RestMethod -Uri "$baseURL/me" -Headers $readHeaders | Out-Null
        throw "revoked impersonation token remained valid"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 401) { throw }
    }

    try {
        Invoke-RestMethod -Method Post -Uri "$baseURL/admin/users/$targetID/impersonation" -Headers $adminHeaders -ContentType "application/json" -Body '{"writeEnabled":true,"confirmation":"wrong@example.com"}' | Out-Null
        throw "wrong confirmation unexpectedly succeeded"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 400) { throw }
    }
    $writeBody = @{ writeEnabled = $true; confirmation = $targetEmail } | ConvertTo-Json -Compress
    $writeSession = Invoke-RestMethod -Method Post -Uri "$baseURL/admin/users/$targetID/impersonation" -Headers $adminHeaders -ContentType "application/json" -Body $writeBody
    $writeHeaders = @{ Authorization = "Bearer $($writeSession.token)" }
    try {
        Invoke-RestMethod -Method Post -Uri "$baseURL/payments/orders" -Headers $writeHeaders -ContentType "application/json" -Body '{}' | Out-Null
        throw "invalid request unexpectedly succeeded"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 400) { throw }
    }
    Invoke-RestMethod -Method Delete -Uri "$baseURL/impersonation" -Headers $writeHeaders | Out-Null

    $auditCount = Invoke-DatabaseScalar "SELECT count(*) FROM audit_logs WHERE actor_user_id='$adminID' AND subject_user_id='$targetID' AND action IN ('impersonation.start','impersonation.write','impersonation.end') AND created_at>now()-interval '10 minutes'"
    if ([int]$auditCount -lt 5) { throw "expected impersonation audit records, got $auditCount" }
    Write-Host "Impersonation HTTP smoke test passed"
} finally {
    Invoke-DatabaseScalar "UPDATE sessions SET revoked_at=now() WHERE id='$adminSessionID' RETURNING id" | Out-Null
}
