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
    $result = & $Compose[0] $Compose[1..($Compose.Length - 1)] exec -T postgres psql -X -v ON_ERROR_STOP=1 -q -U cloudmeter -d cloudmeter -Atc $query
    if ($LASTEXITCODE -ne 0) { throw "database query failed" }
    return (($result | Select-Object -First 1) -as [string]).Trim()
}

function New-Token {
    $bytes = New-Object byte[] 32
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $random.GetBytes($bytes) } finally { $random.Dispose() }
    return [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function Assert-APIError([string]$method, [string]$path, [hashtable]$headers, [string]$body, [int]$status, [string]$code) {
    try {
        Invoke-RestMethod -Method $method -Uri "$baseURL$path" -Headers $headers -ContentType "application/json" -Body $body | Out-Null
        throw "$method $path unexpectedly succeeded"
    } catch {
        if (-not $_.Exception.Response -or [int]$_.Exception.Response.StatusCode -ne $status) { throw }
        $payload = $_.ErrorDetails.Message | ConvertFrom-Json
        if ($payload.error.code -ne $code) { throw "$method $path returned $($payload.error.code), expected $code" }
    }
}

function Assert-RateLimited([string]$path, [string]$body) {
    try {
        Invoke-RestMethod -Method Post -Uri "$baseURL$path" -ContentType "application/json" -Body $body | Out-Null
        throw "POST $path unexpectedly succeeded"
    } catch {
        if (-not $_.Exception.Response -or [int]$_.Exception.Response.StatusCode -ne 429) { throw }
        $payload = $_.ErrorDetails.Message | ConvertFrom-Json
        if ($payload.error.code -ne 'verification_rate_limited') { throw "POST $path returned $($payload.error.code), expected verification_rate_limited" }
        $retryAfter = ''
        try { $retryAfter = [string]$_.Exception.Response.Headers['Retry-After'] } catch { }
        if (-not $retryAfter) {
            try { $retryAfter = [string](($_.Exception.Response.Headers.GetValues('Retry-After') | Select-Object -First 1)) } catch { }
        }
        if ($retryAfter -ne '60') { throw "POST $path did not return Retry-After: 60" }
    }
}

$marker = [guid]::NewGuid().ToString("N").Substring(0, 12)
$smtpContainer = "cm-test-smtp-$marker"
$testEmail = "account-$marker@sub.example.com"
$lockoutEmail = "lockout-$marker@sub.example.com"
$testPassword = "CloudMeter-Account-$marker"
$adminSessionID = ""
$userSessionID = ""
$userID = ""
$announcementID = ""
$smtpStarted = $false
$snapshotsLoaded = $false

$adminID = DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1"
if (-not $adminID) { throw "an active super administrator is required" }
$adminToken = New-Token
$adminSessionID = DB "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$adminID',digest('$adminToken','sha256'),now()+interval '20 minutes') RETURNING id"
$adminHeaders = @{ Authorization = "Bearer $adminToken" }

try {
    $authSnapshot = Invoke-RestMethod -Uri "$baseURL/admin/settings/auth" -Headers $adminHeaders
    $mailSnapshot = Invoke-RestMethod -Uri "$baseURL/admin/settings/mail" -Headers $adminHeaders
    $passwordDigest = DB "SELECT encode(digest(password,'sha256'),'hex') FROM smtp_settings WHERE singleton"
    $snapshotsLoaded = $true

    $apiID = (& $Compose[0] $Compose[1..($Compose.Length - 1)] ps -q api).Trim()
    if (-not $apiID) { throw "API container is not running" }
    $networkLines = docker inspect --format '{{range $name, $config := .NetworkSettings.Networks}}{{println $name}}{{end}}' $apiID
    $dataNetwork = $networkLines | Where-Object { $_ -match 'data_network$' } | Select-Object -First 1
    if (-not $dataNetwork) { throw "API data network was not found" }

    docker run --rm -d --name $smtpContainer --network $dataNetwork mailhog/mailhog:v1.0.1 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "temporary SMTP server failed to start" }
    $smtpStarted = $true
    Start-Sleep -Seconds 2

    $baselineAuth = @{ registrationEnabled = $false; emailVerificationRequired = $false; blockEmailAliases = $true; emailDomainWhitelist = @() } | ConvertTo-Json -Compress
    Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/auth" -Headers $adminHeaders -ContentType "application/json" -Body $baselineAuth | Out-Null
    $disabledMail = @{ enabled = $false; host = ''; port = 587; username = ''; password = ''; fromEmail = ''; fromName = 'CloudMeter'; tlsMode = 'starttls' } | ConvertTo-Json -Compress
    Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/mail" -Headers $adminHeaders -ContentType "application/json" -Body $disabledMail | Out-Null

    $invalidDomain = @{ registrationEnabled = $true; emailVerificationRequired = $false; blockEmailAliases = $true; emailDomainWhitelist = @('example..com') } | ConvertTo-Json -Compress
    Assert-APIError Put '/admin/settings/auth' $adminHeaders $invalidDomain 400 'invalid_email_domain_whitelist'
    $verificationWithoutSMTP = @{ registrationEnabled = $true; emailVerificationRequired = $true; blockEmailAliases = $true; emailDomainWhitelist = @('example.com') } | ConvertTo-Json -Compress
    Assert-APIError Put '/admin/settings/auth' $adminHeaders $verificationWithoutSMTP 409 'smtp_required_for_email_verification'

    $testMail = @{ enabled = $true; host = $smtpContainer; port = 1025; username = ''; password = ''; fromEmail = 'verify@cloudmeter.local'; fromName = 'CloudMeter Verify'; tlsMode = 'none' } | ConvertTo-Json -Compress
    $mailResult = Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/mail" -Headers $adminHeaders -ContentType "application/json" -Body $testMail
    if (-not $mailResult.ready) { throw "saved SMTP settings were not reported ready" }
    Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/auth" -Headers $adminHeaders -ContentType "application/json" -Body $verificationWithoutSMTP | Out-Null
    Assert-APIError Put '/admin/settings/mail' $adminHeaders $disabledMail 409 'email_verification_requires_smtp'

    Assert-APIError Post '/auth/verification-code' @{} '{"email":"alias+tag@example.com"}' 403 'email_policy_blocked'
    Assert-APIError Post '/auth/verification-code' @{} '{"email":"alias.tag@example.com"}' 403 'email_policy_blocked'
    Assert-APIError Post '/auth/verification-code' @{} '{"email":"outside@example.net"}' 403 'email_policy_blocked'
    $verificationRequest = @{ email = $testEmail } | ConvertTo-Json -Compress
    $verification = Invoke-RestMethod -Method Post -Uri "$baseURL/auth/verification-code" -ContentType "application/json" -Body $verificationRequest
    if (-not $verification.sent -or -not $verification.required) { throw "verification email was not sent" }
    Assert-RateLimited '/auth/verification-code' $verificationRequest

    $mailJSON = docker run --rm --network $dataNetwork busybox:1.37 wget -Y off -qO- "http://${smtpContainer}:8025/api/v2/messages"
    if ($LASTEXITCODE -ne 0) { throw "could not read the temporary SMTP mailbox" }
    $mailbox = ($mailJSON -join "`n") | ConvertFrom-Json
    $message = $mailbox.items | Where-Object { $_.Content.Headers.To -contains $testEmail } | Select-Object -First 1
    $codeMatch = [regex]::Match([string]$message.Content.Body, '\b([0-9]{6})\b')
    if (-not $message -or -not $codeMatch.Success) { throw "verification code was not present in the email" }
    $code = $codeMatch.Groups[1].Value

    $lockoutRequest = @{ email = $lockoutEmail } | ConvertTo-Json -Compress
    Invoke-RestMethod -Method Post -Uri "$baseURL/auth/verification-code" -ContentType "application/json" -Body $lockoutRequest | Out-Null
    $mailJSON = docker run --rm --network $dataNetwork busybox:1.37 wget -Y off -qO- "http://${smtpContainer}:8025/api/v2/messages"
    if ($LASTEXITCODE -ne 0) { throw "could not read the temporary SMTP mailbox for lockout test" }
    $mailbox = ($mailJSON -join "`n") | ConvertFrom-Json
    $lockoutMessage = $mailbox.items | Where-Object { $_.Content.Headers.To -contains $lockoutEmail } | Select-Object -First 1
    $lockoutCodeMatch = [regex]::Match([string]$lockoutMessage.Content.Body, '\b([0-9]{6})\b')
    if (-not $lockoutMessage -or -not $lockoutCodeMatch.Success) { throw "lockout verification code was not present in the email" }
    $lockoutCode = $lockoutCodeMatch.Groups[1].Value
    $wrongCode = if ($lockoutCode -eq '000000') { '999999' } else { '000000' }
    for ($attempt = 1; $attempt -le 5; $attempt++) {
        $wrongRegistration = @{ displayName = "Lockout Verify $marker"; email = $lockoutEmail; password = $testPassword; code = $wrongCode } | ConvertTo-Json -Compress
        Assert-APIError Post '/auth/register' @{} $wrongRegistration 400 'invalid_verification_code'
    }
    $correctAfterLockout = @{ displayName = "Lockout Verify $marker"; email = $lockoutEmail; password = $testPassword; code = $lockoutCode } | ConvertTo-Json -Compress
    Assert-APIError Post '/auth/register' @{} $correctAfterLockout 400 'invalid_verification_code'
    $lockoutState = DB "SELECT attempt_count::text || '|' || (consumed_at IS NOT NULL)::text FROM email_verification_codes WHERE lower(email)=lower('$lockoutEmail') ORDER BY created_at DESC LIMIT 1"
    if ($lockoutState -ne '5|true') { throw "verification code lockout state was $lockoutState, expected 5|true" }

    $registerBody = @{ displayName = "Account Verify $marker"; email = $testEmail; password = $testPassword; code = $code } | ConvertTo-Json -Compress
    $registered = Invoke-RestMethod -Method Post -Uri "$baseURL/auth/register" -ContentType "application/json" -Body $registerBody
    if (-not $registered.registered) { throw "verified registration failed" }
    $loginBody = @{ email = $testEmail; password = $testPassword } | ConvertTo-Json -Compress
    $login = Invoke-RestMethod -Method Post -Uri "$baseURL/auth/login" -ContentType "application/json" -Body $loginBody
    $userHeaders = @{ Authorization = "Bearer $($login.token)" }
    $userID = $login.user.id
    $userSessionID = DB "SELECT id FROM sessions WHERE user_id='$userID' AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 1"

    Assert-APIError Post '/admin/announcements' $adminHeaders '{"title":"","content":"bad","severity":"invalid","published":true}' 400 'validation_failed'
    $announcementBody = @{ title = "Account feature verify $marker"; content = 'SMTP, registration policy and announcement verification'; severity = 'warning'; published = $true } | ConvertTo-Json -Compress
    $announcement = Invoke-RestMethod -Method Post -Uri "$baseURL/admin/announcements" -Headers $adminHeaders -ContentType "application/json" -Body $announcementBody
    $announcementID = $announcement.id
    $visible = Invoke-RestMethod -Uri "$baseURL/announcements" -Headers $userHeaders
    if ($visible.announcements.id -notcontains $announcementID) { throw "published announcement was not visible to the user" }
    Invoke-RestMethod -Method Patch -Uri "$baseURL/admin/announcements/$announcementID" -Headers $adminHeaders -ContentType "application/json" -Body '{"published":false}' | Out-Null
    $hidden = Invoke-RestMethod -Uri "$baseURL/announcements" -Headers $userHeaders
    if ($hidden.announcements.id -contains $announcementID) { throw "unpublished announcement remained visible to the user" }

    $auditCount = DB "SELECT count(*) FROM audit_logs WHERE actor_user_id='$adminID' AND action IN ('auth.policy.update','smtp.settings.update','announcement.create','announcement.publish.update') AND created_at>now()-interval '20 minutes'"
    if ([int]$auditCount -lt 6) { throw "account feature audit records are incomplete" }
    Write-Host "SMTP invariant, rate limit, verification lockout, domain and alias policy, registration, login, announcement visibility and audit smoke test passed"
} finally {
    if ($snapshotsLoaded) {
        try {
            $safeAuth = @{ registrationEnabled = $false; emailVerificationRequired = $false; blockEmailAliases = [bool]$authSnapshot.blockEmailAliases; emailDomainWhitelist = @($authSnapshot.emailDomainWhitelist) } | ConvertTo-Json -Compress
            Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/auth" -Headers $adminHeaders -ContentType "application/json" -Body $safeAuth | Out-Null
            $restoreMail = @{ enabled = [bool]$mailSnapshot.enabled; host = [string]$mailSnapshot.host; port = [int]$mailSnapshot.port; username = [string]$mailSnapshot.username; password = ''; fromEmail = [string]$mailSnapshot.fromEmail; fromName = [string]$mailSnapshot.fromName; tlsMode = [string]$mailSnapshot.tlsMode } | ConvertTo-Json -Compress
            Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/mail" -Headers $adminHeaders -ContentType "application/json" -Body $restoreMail | Out-Null
            $restoreAuth = @{ registrationEnabled = [bool]$authSnapshot.registrationEnabled; emailVerificationRequired = [bool]$authSnapshot.emailVerificationRequired; blockEmailAliases = [bool]$authSnapshot.blockEmailAliases; emailDomainWhitelist = @($authSnapshot.emailDomainWhitelist) } | ConvertTo-Json -Compress
            Invoke-RestMethod -Method Put -Uri "$baseURL/admin/settings/auth" -Headers $adminHeaders -ContentType "application/json" -Body $restoreAuth | Out-Null
            $restoredDigest = DB "SELECT encode(digest(password,'sha256'),'hex') FROM smtp_settings WHERE singleton"
            if ($restoredDigest -ne $passwordDigest) { throw "SMTP password ciphertext changed during verification" }
        } catch { Write-Warning "configuration restore failed: $($_.Exception.Message)" }
    }
    if ($announcementID) {
        try { DB "DELETE FROM announcements WHERE id='$announcementID' RETURNING id" | Out-Null } catch { Write-Warning "temporary announcement cleanup failed" }
    }
    if ($userID) {
        try { DB "UPDATE users SET status='suspended',updated_at=now() WHERE id='$userID' RETURNING id" | Out-Null } catch { Write-Warning "temporary user suspension failed" }
    }
    try { DB "DELETE FROM email_verification_codes WHERE lower(email) IN (lower('$testEmail'),lower('$lockoutEmail')) RETURNING id" | Out-Null } catch { Write-Warning "temporary verification code cleanup failed" }
    if ($adminSessionID -or $userSessionID) {
        $sessionIDs = @($adminSessionID, $userSessionID) | Where-Object { $_ } | ForEach-Object { "'$_'" }
        try { DB "UPDATE sessions SET revoked_at=now() WHERE id IN ($($sessionIDs -join ',')) RETURNING id" | Out-Null } catch { Write-Warning "temporary session cleanup failed" }
    }
    if ($smtpStarted) { docker rm -f $smtpContainer | Out-Null }
}
