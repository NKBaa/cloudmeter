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
    $result = & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc $query
    if ($LASTEXITCODE -ne 0) { throw "database query failed" }
    return (($result | Select-Object -First 1) -as [string]).Trim()
}
function New-Session([string]$userID) {
    $bytes = New-Object byte[] 32
    [Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
    $token = (($bytes | ForEach-Object { $_.ToString('x2') }) -join '')
    $id = DB "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$userID',digest('$token','sha256'),now()+interval '15 minutes') RETURNING id"
    return [pscustomobject]@{ ID = $id; Token = $token }
}

$marker = [guid]::NewGuid().ToString("N")
$userIDs = (DB "SELECT string_agg(id::text,',') FROM (SELECT id FROM users WHERE status='active' ORDER BY created_at LIMIT 2) q").Split(',')
if ($userIDs.Count -lt 2) { throw "two active users are required" }
$first = New-Session $userIDs[0]
$second = New-Session $userIDs[1]
$firstNotification = DB "INSERT INTO user_notifications(user_id,kind,severity,event_key,title,content) VALUES('$($userIDs[0])','low_balance','warning','access-first/$marker','Access marker A','test') RETURNING id"
$secondNotification = DB "INSERT INTO user_notifications(user_id,kind,severity,event_key,title,content) VALUES('$($userIDs[1])','low_balance','warning','access-second/$marker','Access marker B','test') RETURNING id"
try {
    try {
        Invoke-WebRequest -UseBasicParsing "$baseURL/notifications" | Out-Null
        throw "unauthenticated notification request unexpectedly succeeded"
    } catch {
        if ([int]$_.Exception.Response.StatusCode -ne 401) { throw }
    }
    $list = Invoke-RestMethod "$baseURL/notifications" -Headers @{ Authorization = "Bearer $($first.Token)" }
    if (-not ($list.notifications.id -contains $firstNotification) -or ($list.notifications.id -contains $secondNotification)) {
        throw "notification list account isolation failed"
    }
    try {
        Invoke-WebRequest -UseBasicParsing -Method Patch "$baseURL/notifications/$secondNotification/read" -Headers @{ Authorization = "Bearer $($first.Token)" } | Out-Null
        throw "cross-account notification update unexpectedly succeeded"
    } catch {
        if ([int]$_.Exception.Response.StatusCode -ne 404) { throw }
    }
    $owned = Invoke-RestMethod -Method Patch "$baseURL/notifications/$secondNotification/read" -Headers @{ Authorization = "Bearer $($second.Token)" }
    if (-not $owned.readAt) { throw "notification owner could not mark notification read" }
    Write-Host "Notification authentication, list isolation, cross-account denial and owner update smoke test passed"
} finally {
    DB "UPDATE sessions SET revoked_at=now() WHERE id IN ('$($first.ID)','$($second.ID)'); DELETE FROM user_notifications WHERE event_key IN ('access-first/$marker','access-second/$marker')" | Out-Null
}
