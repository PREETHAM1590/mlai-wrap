# windows-smoke.ps1 - builds mlai-wrap.exe and checks the packaged executable on
# Windows: version and build identity, startup, the discovery file, `status`,
# direct-only refusing without a CLI run, and that a killed bridge leaves no
# usable discovery file. No ML AI account is needed: mlai is not installed.
# Runs in CI (pwsh) and locally:  powershell -File scripts\windows-smoke.ps1
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) ("mlai-wrap-smoke-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force $work | Out-Null
$exe = Join-Path $work 'mlai-wrap.exe'
$port = 18197
$fails = 0
function Check([bool]$ok, [string]$what) {
    if ($ok) { Write-Host "ok   $what" } else { Write-Host "FAIL $what"; $script:fails++ }
}

Push-Location $root
try { go build -trimpath -ldflags "-X main.packageVersion=smoke" -o $exe . } finally { Pop-Location }
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

# A private home, so the real ~/mlai-bridge is never touched.
$env:USERPROFILE = Join-Path $work 'home'
$env:HOME = $env:USERPROFILE
New-Item -ItemType Directory -Force $env:USERPROFILE | Out-Null
$env:MLAI_BIN = Join-Path $work 'no-such-mlai.exe'
$env:MLAI_API_KEY = $null
$env:MLAI_BRIDGE_URL = $null

$v = & $exe version
Check ($v -like 'mlai-wrap smoke (mlai-bridge *') "version prints the build identity: $v"

$log = Join-Path $work 'serve.log'
$p = Start-Process -FilePath $exe -ArgumentList 'serve', '-port', "$port", '-cache-ttl', '0' -PassThru -NoNewWindow `
    -RedirectStandardOutput $log -RedirectStandardError "$log.err"
try {
    $up = $false
    for ($i = 0; $i -lt 60 -and -not $up; $i++) {
        try { Invoke-RestMethod "http://127.0.0.1:$port/healthz" -TimeoutSec 20 | Out-Null; $up = $true } catch { Start-Sleep -Milliseconds 250 }
    }
    Check $up 'serve answers /healthz'

    $disc = Join-Path $env:USERPROFILE 'mlai-bridge\serve.json'
    Check (Test-Path $disc) 'serve writes the discovery file'

    $st = & $exe status 2>&1 | Out-String
    Check ($LASTEXITCODE -eq 0 -and $st -match "127\.0\.0\.1:$port" -and $st -match 'build:\s+mlai-wrap smoke') "status finds the running bridge and shows its build"

    $json = & $exe status --json | Out-String | ConvertFrom-Json
    Check ($json.build.package -eq 'smoke' -and $json.direct -eq $true) 'status --json'

    $body = '{"model":"m","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}'
    $t0 = Get-Date
    $code = 0; $msg = ''
    try {
        Invoke-RestMethod "http://127.0.0.1:$port/v1/messages" -Method Post -ContentType 'application/json' `
            -Headers @{ 'X-MLAI-Direct-Only' = 'true' } -Body $body -TimeoutSec 120 | Out-Null
    } catch {
        $code = [int]$_.Exception.Response.StatusCode
        $msg = "$($_.ErrorDetails.Message)"
    }
    $secs = ((Get-Date) - $t0).TotalSeconds
    Check ($code -eq 503 -and $msg -match 'direct-only') ("direct-only answers 503 when the gateway path is unavailable ({0:N1}s)" -f $secs)
    Check ($secs -lt 60) 'direct-only answers within the request budget'
} finally {
    Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
    $p.WaitForExit(10000) | Out-Null
}

# Killed without cleanup: the discovery file stays, but nothing proves it, so
# status does not use it (it may still find another bridge on the default port).
$st = & $exe status 2>&1 | Out-String
Check ($st -notmatch "127\.0\.0\.1:$port") 'the stale discovery file of a killed bridge is not trusted'
Check (-not (Get-Process -Id $p.Id -ErrorAction SilentlyContinue)) 'no bridge process is left'

if ($fails -gt 0) {
    Write-Host "--- serve log"; Get-Content $log, "$log.err" -ErrorAction SilentlyContinue
    throw "$fails smoke check(s) failed"
}
Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
Write-Host 'windows smoke test passed'
