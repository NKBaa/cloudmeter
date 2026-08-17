$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot; Set-Location $Root
$Compose = @('docker','compose'); if ($env:COMPOSE_PROJECT_NAME) { $Compose += @('--project-name',$env:COMPOSE_PROJECT_NAME) }; $Compose += @('--env-file','.env','-f','deploy/compose.yaml'); if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$port = if ($env:PLATFORM_PORT) {[int]$env:PLATFORM_PORT} else {8080}; $api="http://127.0.0.1:$port/api"
function DB([string]$q) { $v=& $Compose[0] $Compose[1..($Compose.Length-1)] exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc $q; if($LASTEXITCODE-ne 0){throw 'database query failed'}; (($v|Select-Object -First 1)-as[string]).Trim() }
function Session([string]$uid) { $b=New-Object byte[] 32; $rng=[Security.Cryptography.RandomNumberGenerator]::Create(); try {$rng.GetBytes($b)} finally {$rng.Dispose()}; $t=([Convert]::ToBase64String($b)).TrimEnd('=').Replace('+','-').Replace('/','_'); $id=DB "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$uid',digest('$t','sha256'),now()+interval '15 minutes') RETURNING id"; [pscustomobject]@{ID=$id;Token=$t} }
function Expect([scriptblock]$fn,[int]$code) { try { & $fn | Out-Null; throw "expected HTTP $code" } catch { if([int]$_.Exception.Response.StatusCode.value__ -ne $code){throw} } }
$admin=DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' LIMIT 1"; $user=DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='user' AND u.status='active' LIMIT 1"; if(!$admin -or !$user){throw 'active admin and user required'}
$a=Session $admin; $u=Session $user; $ah=@{Authorization="Bearer $($a.Token)"}; $uh=@{Authorization="Bearer $($u.Token)"}; $key="provider-op-$([guid]::NewGuid().ToString('N'))"
try {
  $order=Invoke-RestMethod -Method Post "$api/payments/orders" -Headers $uh -ContentType 'application/json' -Body (@{amountCents=1;provider='manual';idempotencyKey=$key}|ConvertTo-Json -Compress)
  Expect { Invoke-WebRequest -UseBasicParsing -Method Post "$api/admin/payments/orders/$($order.orderId)/query" -Headers $uh -ContentType 'application/json' -Body '{}' } 403
  $q=Invoke-RestMethod -Method Post "$api/admin/payments/orders/$($order.orderId)/query" -Headers $ah -ContentType 'application/json' -Body '{}'; if($q.providerStatus-ne 'pending'){throw 'manual query did not return pending'}
  $c=Invoke-RestMethod -Method Post "$api/admin/payments/orders/$($order.orderId)/close" -Headers $ah -ContentType 'application/json' -Body '{}'; $c2=Invoke-RestMethod -Method Post "$api/admin/payments/orders/$($order.orderId)/close" -Headers $ah -ContentType 'application/json' -Body '{}'; if($c.status-ne 'closed' -or $c2.status-ne 'closed'){throw 'close replay failed'}
  $ops=[int](DB "SELECT count(*) FROM payment_provider_operations WHERE order_id='$($order.orderId)' AND result='succeeded'"); if($ops -lt 3){throw 'operation records missing'}
  Write-Host 'Payment provider query, close, idempotent replay and RBAC verification passed'
} finally { DB "UPDATE sessions SET revoked_at=now() WHERE id IN ('$($a.ID)','$($u.ID)')"|Out-Null }
