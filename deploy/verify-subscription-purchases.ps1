$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
$Compose = @('docker','compose')
if ($env:COMPOSE_PROJECT_NAME) { $Compose += @('--project-name',$env:COMPOSE_PROJECT_NAME) }
$Compose += @('--env-file','.env','-f','deploy/compose.yaml')
if ($env:CLOUDMETER_COMPOSE_OVERRIDE) { $Compose += @('-f', $env:CLOUDMETER_COMPOSE_OVERRIDE) }
$port = if ($env:PLATFORM_PORT) {[int]$env:PLATFORM_PORT} else {8080}
$api = "http://127.0.0.1:$port/api"

function DB([string]$query) {
  $value = & $Compose[0] $Compose[1..($Compose.Length-1)] exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc $query
  if ($LASTEXITCODE -ne 0) { throw 'database query failed' }
  (($value | Select-Object -First 1) -as [string]).Trim()
}
function Session([string]$userID) {
  $bytes = New-Object byte[] 32
  $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
  $token = ([Convert]::ToBase64String($bytes)).TrimEnd('=').Replace('+','-').Replace('/','_')
  $id = DB "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$userID',digest('$token','sha256'),now()+interval '20 minutes') RETURNING id"
  [pscustomobject]@{ ID=$id; Token=$token }
}
function Status([string]$method,[string]$uri,[hashtable]$headers=$null,[string]$body='') {
  try {
    $args = @{UseBasicParsing=$true;Method=$method;Uri=$uri}
    if ($headers) { $args.Headers=$headers }
    if ($body) { $args.ContentType='application/json';$args.Body=$body }
    [int](Invoke-WebRequest @args).StatusCode
  } catch { [int]$_.Exception.Response.StatusCode }
}
function Wait-DB([string]$query,[string]$expected,[int]$seconds=50) {
  $deadline=(Get-Date).AddSeconds($seconds); $value=''
  do { $value=DB $query; if($value -eq $expected){return}; Start-Sleep -Seconds 2 } while((Get-Date)-lt $deadline)
  throw "timed out waiting for $expected; last value was $value"
}

$adminID = DB "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' LIMIT 1"
if (!$adminID) { throw 'active super administrator required' }
$adminSession = Session $adminID
$adminHeaders = @{Authorization="Bearer $($adminSession.Token)"}
$userSession = $null
$userID = ''
$planID = ''
$lowerPlanID = ''
$jobs = @()
$marker = [guid]::NewGuid().ToString('N').Substring(0,12)
$password = "Subscription-$marker-Password!"
try {
  $user = Invoke-RestMethod -Method Post "$api/admin/users" -Headers $adminHeaders -ContentType 'application/json' -Body (@{email="subscription-$marker@example.invalid";password=$password;displayName='套餐交易验收';role='user'}|ConvertTo-Json -Compress)
  $userID = $user.id
  $userSession = Session $userID
  $userHeaders = @{Authorization="Bearer $($userSession.Token)"}
  if ((Status GET "$api/subscriptions/plans") -ne 401) { throw 'unauthenticated plan list was not rejected' }
  if ((Status GET "$api/admin/plans" $userHeaders) -ne 403) { throw 'normal user reached admin plan API' }

  $plan = Invoke-RestMethod -Method Post "$api/admin/plans" -Headers $adminHeaders -ContentType 'application/json' -Body (@{code="verify-cycle-$marker";name="周期验收 $marker"}|ConvertTo-Json -Compress)
  $lowerPlan = Invoke-RestMethod -Method Post "$api/admin/plans" -Headers $adminHeaders -ContentType 'application/json' -Body (@{code="verify-lower-$marker";name="降级验收 $marker"}|ConvertTo-Json -Compress)
  $planID = $plan.id
  $lowerPlanID = $lowerPlan.id
  $version = @{cyclePriceCents=1000;apps=2;cpuCores=2;memoryGiB=2;systemDiskGiB=10;dataDiskGiB=10;backupStorageGiB=0;backupOperationsPerMonth=0;concurrentDeployments=1;publicIngresses=1;ingressOverageEnabled=$false;egressGiB=1;egressOverageEnabled=$false;creditGrantCents=100;allowedProductIds=@();effectiveAt=(Get-Date).ToUniversalTime().ToString('o')}
  $v1 = Invoke-RestMethod -Method Post "$api/admin/plans/$($plan.id)/versions" -Headers $adminHeaders -ContentType 'application/json' -Body ($version|ConvertTo-Json -Compress)
  $version.cyclePriceCents=500; $version.creditGrantCents=0
  $lower = Invoke-RestMethod -Method Post "$api/admin/plans/$($lowerPlan.id)/versions" -Headers $adminHeaders -ContentType 'application/json' -Body ($version|ConvertTo-Json -Compress)

  $adminCatalog=Invoke-RestMethod -Method Get "$api/admin/plans" -Headers $adminHeaders
  $createdPlan=$adminCatalog.plans|Where-Object{$_.id -eq $plan.id}
  if ($null -eq $createdPlan -or $createdPlan.purchaseEnabled -ne $false) { throw 'new plan was not disabled for self-service purchase by default' }
  $hiddenCatalog=Invoke-RestMethod -Method Get "$api/subscriptions/plans" -Headers $userHeaders
  if ($hiddenCatalog.plans|Where-Object{$_.planId -in @($plan.id,$lowerPlan.id)}) { throw 'disabled plans were visible to a user without a subscription' }
  $unavailableBody=@{planVersionId=$v1.id;idempotencyKey=[guid]::NewGuid().ToString()}|ConvertTo-Json -Compress
  if ((Status POST "$api/subscriptions/purchases" $userHeaders $unavailableBody) -ne 404) { throw 'disabled plan purchase was not rejected' }
  if ((Status PATCH "$api/admin/plans/$($plan.id)/availability" $userHeaders (@{enabled=$true}|ConvertTo-Json -Compress)) -ne 403) { throw 'normal user changed plan availability' }
  Invoke-RestMethod -Method Patch "$api/admin/plans/$($plan.id)/availability" -Headers $adminHeaders -ContentType 'application/json' -Body (@{enabled=$true}|ConvertTo-Json -Compress) | Out-Null
  Invoke-RestMethod -Method Patch "$api/admin/plans/$($lowerPlan.id)/availability" -Headers $adminHeaders -ContentType 'application/json' -Body (@{enabled=$true}|ConvertTo-Json -Compress) | Out-Null

  $failedBody=@{planVersionId=$v1.id;idempotencyKey=[guid]::NewGuid().ToString()}|ConvertTo-Json -Compress
  $insufficientStatus = Status POST "$api/subscriptions/purchases" $userHeaders $failedBody
  if ($insufficientStatus -ne 409) { throw "insufficient balance purchase was not rejected (got HTTP $insufficientStatus)" }
  if ((DB "SELECT count(*) FROM subscription_purchases WHERE user_id='$userID' AND status='insufficient_funds'") -ne '1') { throw 'failed purchase attempt was not recorded' }
  if ((DB "SELECT count(*) FROM wallet_ledger_entries e JOIN wallets w ON w.id=e.wallet_id WHERE w.user_id='$userID' AND e.business_type='subscription'") -ne '0') { throw 'failed purchase wrote a wallet debit' }

  Invoke-RestMethod -Method Post "$api/admin/users/$userID/wallet/adjust" -Headers $adminHeaders -ContentType 'application/json' -Body (@{amountCents=5000;businessRef="subscription-verify/$marker";note='套餐交易专项验收'}|ConvertTo-Json -Compress) | Out-Null
  $purchaseBody=@{planVersionId=$v1.id;idempotencyKey=[guid]::NewGuid().ToString()}|ConvertTo-Json -Compress
  $jobs=1..6|ForEach-Object { Start-Job -ScriptBlock { param($uri,$token,$body) try { [int](Invoke-WebRequest -UseBasicParsing -Method Post $uri -Headers @{Authorization="Bearer $token"} -ContentType 'application/json' -Body $body).StatusCode } catch { [int]$_.Exception.Response.StatusCode } } -ArgumentList "$api/subscriptions/purchases",$userSession.Token,$purchaseBody }
  $statuses=@($jobs|Wait-Job|Receive-Job)
  if ($statuses.Count-ne 6 -or @($statuses|Where-Object{$_-notin @(200,201)}).Count -or @($statuses|Where-Object{$_-eq 201}).Count-ne 1) { throw "concurrent purchase statuses were $($statuses -join ',')" }
  $jobs|Remove-Job -Force; $jobs=@()
  if ((DB "SELECT count(*) FROM subscription_purchases WHERE user_id='$userID' AND status='succeeded'") -ne '1') { throw 'concurrent purchase was not idempotent' }
  if ((DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'") -ne '4000') { throw 'initial subscription debit was incorrect' }
  $initialEnds = DB "SELECT extract(epoch FROM ends_at)::bigint FROM user_subscriptions WHERE user_id='$userID'"

  $version.cyclePriceCents=1800; $version.creditGrantCents=200
  $v2 = Invoke-RestMethod -Method Post "$api/admin/plans/$($plan.id)/versions" -Headers $adminHeaders -ContentType 'application/json' -Body ($version|ConvertTo-Json -Compress)
  $catalog=Invoke-RestMethod -Method Get "$api/subscriptions/plans" -Headers $userHeaders
  $quote=$catalog.plans|Where-Object{$_.planVersionId -eq $v2.id}
  if ($quote.purchaseAction-ne'upgrade' -or $quote.payableCents-ne800) { throw 'upgrade quote did not charge the positive difference' }
  $upgrade=Invoke-RestMethod -Method Post "$api/subscriptions/purchases" -Headers $userHeaders -ContentType 'application/json' -Body (@{planVersionId=$v2.id;idempotencyKey=[guid]::NewGuid().ToString()}|ConvertTo-Json -Compress)
  if ($upgrade.purchase.amountCents-ne800 -or (DB "SELECT extract(epoch FROM ends_at)::bigint FROM user_subscriptions WHERE user_id='$userID'")-ne$initialEnds) { throw 'upgrade debit or term preservation failed' }

  $downgrade=Invoke-RestMethod -Method Post "$api/subscriptions/purchases" -Headers $userHeaders -ContentType 'application/json' -Body (@{planVersionId=$lower.id;idempotencyKey=[guid]::NewGuid().ToString()}|ConvertTo-Json -Compress)
  if ($downgrade.purchase.amountCents-ne0 -or $downgrade.purchase.action-ne'downgrade' -or (DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'")-ne'3200') { throw 'downgrade refunded or debited the wallet' }
  Invoke-RestMethod -Method Patch "$api/admin/plans/$($lowerPlan.id)/availability" -Headers $adminHeaders -ContentType 'application/json' -Body (@{enabled=$false}|ConvertTo-Json -Compress) | Out-Null
  $disabledCurrentCatalog=Invoke-RestMethod -Method Get "$api/subscriptions/plans" -Headers $userHeaders
  $renewalQuote=$disabledCurrentCatalog.plans|Where-Object{$_.planId -eq $lowerPlan.id}
  if ($null -eq $renewalQuote -or $renewalQuote.purchaseAction-ne'renewal') { throw 'disabled current plan was not retained as renewable' }
  $renew=Invoke-RestMethod -Method Post "$api/subscriptions/purchases" -Headers $userHeaders -ContentType 'application/json' -Body (@{planVersionId=$lower.id;idempotencyKey=[guid]::NewGuid().ToString()}|ConvertTo-Json -Compress)
  if ($renew.purchase.amountCents-ne500 -or $renew.purchase.action-ne'renewal' -or (DB "SELECT balance_cents FROM wallets WHERE user_id='$userID'")-ne'2700') { throw 'renewal debit failed' }

  $statementTotal=DB "SELECT coalesce(sum(amount_cents),0) FROM subscription_bill_items i JOIN bills b ON b.id=i.bill_id WHERE b.user_id='$userID'"
  $ledgerTotal=DB "SELECT coalesce(-sum(e.amount_cents),0) FROM wallet_ledger_entries e JOIN wallets w ON w.id=e.wallet_id WHERE w.user_id='$userID' AND e.business_type='subscription'"
  if ($statementTotal-ne'2300' -or $ledgerTotal-ne'2300') { throw 'subscription statement and wallet totals diverged' }
  if ((DB "SELECT count(*) FROM subscription_bill_items i JOIN bills b ON b.id=i.bill_id WHERE b.user_id='$userID'")-ne'4') { throw 'subscription statement items are incomplete' }
  if ([int](DB "SELECT count(*) FROM audit_logs WHERE subject_user_id='$userID' AND action='subscription.purchase'")-lt4) { throw 'subscription purchase audit history is incomplete' }
  if ([int](DB "SELECT count(*) FROM audit_logs WHERE resource_id IN ('$planID','$lowerPlanID') AND action='plan.availability.update'")-lt3) { throw 'plan availability audit history is incomplete' }

  DB "UPDATE user_subscriptions SET status='active',ends_at=now()+interval '1 day',grace_ends_at=NULL WHERE user_id='$userID'" | Out-Null
  Wait-DB "SELECT count(*) FROM user_notifications WHERE user_id='$userID' AND kind='subscription_expiring'" '1'
  DB "UPDATE user_subscriptions SET ends_at=now()-interval '1 second' WHERE user_id='$userID'" | Out-Null
  Wait-DB "SELECT status FROM user_subscriptions WHERE user_id='$userID'" 'grace_period'
  DB "UPDATE user_subscriptions SET grace_ends_at=now()-interval '1 second' WHERE user_id='$userID'" | Out-Null
  Wait-DB "SELECT status FROM user_subscriptions WHERE user_id='$userID'" 'expired'
  if ((DB "SELECT count(*) FROM user_notifications WHERE user_id='$userID' AND kind IN ('subscription_grace','subscription_expired')")-ne'2') { throw 'subscription lifecycle notifications are incomplete' }
  if ((DB "SELECT count(*) FROM audit_logs WHERE subject_user_id='$userID' AND action IN ('subscription.grace_period','subscription.expire')")-ne'2') { throw 'subscription lifecycle audit history is incomplete' }
  Invoke-RestMethod -Method Patch "$api/admin/plans/$planID/availability" -Headers $adminHeaders -ContentType 'application/json' -Body (@{enabled=$false}|ConvertTo-Json -Compress) | Out-Null
  Write-Host 'Plan availability, subscription purchase, concurrency idempotency, price difference, no-refund downgrade, disabled-plan renewal, statement, notification, audit and Worker lifecycle verification passed'
} finally {
  if ($jobs) { $jobs|Remove-Job -Force -ErrorAction SilentlyContinue }
  if ($userSession) { DB "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($userSession.ID)'" | Out-Null }
  DB "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$($adminSession.ID)'" | Out-Null
  if ($userID) { DB "UPDATE users SET status='suspended',updated_at=now() WHERE id='$userID'" | Out-Null }
  if ($planID -or $lowerPlanID) {
    $ids=@($planID,$lowerPlanID)|Where-Object{$_}
    DB "UPDATE plans SET purchase_enabled=false WHERE id IN ('$($ids -join "','")')" | Out-Null
  }
}
