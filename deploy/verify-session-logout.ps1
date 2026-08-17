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
    $token = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    $escaped = $token.Replace("'", "''")
    $id = Invoke-Db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$UserID',digest('$escaped','sha256'),now()+interval '10 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}

function Get-Status([string]$Method, [string]$Uri, [hashtable]$Headers = $null) {
    try {
        $params = @{ UseBasicParsing = $true; Method = $Method; Uri = $Uri }
        if ($Headers) { $params.Headers = $Headers }
        return [int](Invoke-WebRequest @params).StatusCode
    } catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode.value__ }
        throw
    }
}

$adminID = Invoke-Db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID) { throw 'an active super administrator is required' }
$session = New-Session $adminID
$headers = @{ Authorization = "Bearer $($session.Token)" }
try {
    if ((Get-Status 'GET' "$Api/me" $headers) -ne 200) { throw 'authenticated session was not accepted before logout' }
    if ((Get-Status 'POST' "$Api/auth/logout" $headers) -ne 204) { throw 'logout endpoint did not return 204' }
    if ((Get-Status 'GET' "$Api/me" $headers) -ne 401) { throw 'revoked token remained usable after logout' }
    if ((Invoke-Db "SELECT (revoked_at IS NOT NULL)::text FROM sessions WHERE id='$($session.ID)'") -ne 'true') { throw 'logout did not persist revoked_at' }
    if ([int](Invoke-Db "SELECT count(*) FROM audit_logs WHERE action='auth.logout' AND resource_id='$($session.ID)'") -lt 1) { throw 'auth.logout audit record was not written' }
    Write-Host "Session logout smoke test passed (session $($session.ID))"
} finally {
    Invoke-Db "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($session.ID)'" | Out-Null
}
