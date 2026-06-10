# Universal installer for Windows — downloads the .msi from GitHub
# release and installs silently (per-user, no UAC). Detects arch from
# PROCESSOR_ARCHITECTURE.
#
# Public repo:   iwr -useb <url>/install.ps1 | iex
# Private repo:  $env:TOKEN='ghp_xxx'; iwr -useb -Headers @{Authorization="Bearer $env:TOKEN"} <url>/install.ps1 | iex
# Override app:  $env:APP='myapp'; $env:REPO='org/myapp'; iwr -useb <url>/install.ps1 | iex

$ErrorActionPreference = 'Stop'

$App   = if ($env:APP)   { $env:APP }   else { 'wick-agent' }   # override: $env:APP='myapp'
$Repo  = if ($env:REPO)  { $env:REPO }  else { 'yogasw/wick' }  # override: $env:REPO='owner/myapp'
$Token = if ($env:TOKEN) { $env:TOKEN } else { '' }

$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

$Headers = @{}
if ($Token) { $Headers['Authorization'] = "Bearer $Token" }

function Start-Agent {
  $Candidates = @()
  $Cmd = Get-Command $App -ErrorAction SilentlyContinue
  if ($Cmd) { $Candidates += $Cmd.Source }
  $Candidates += (Join-Path $env:LOCALAPPDATA "Programs\$App\$App.exe")

  $Exe = $Candidates | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
  if (-not $Exe) {
    Write-Warning "could not find $App — skipping auto-start"
    return
  }

  Write-Host "-> starting $App..."
  try {
    & $Exe start
    Write-Host "OK $App started"
  } catch {
    Write-Warning "$App start failed — install completed, run '$Exe start' manually to retry"
  }
}

$Tag = if ($env:VERSION -and $env:VERSION -ne 'latest') {
  $env:VERSION
} else {
  (Invoke-RestMethod -Headers $Headers "https://api.github.com/repos/$Repo/releases/latest").tag_name
}
if (-not $Tag) { throw "could not resolve latest tag for $Repo" }
$Ver = $Tag.TrimStart('v')

# Probe installed version — skip download/msiexec when already at target.
# Keeps re-runs config-only (no UAC prompt, no MSI churn). Falls back to
# install when probe fails or the binary isn't on PATH.
$Installed = $null
$Cmd = Get-Command $App -ErrorAction SilentlyContinue
if ($Cmd) {
  try {
    $Installed = (& $App version 2>$null | Select-Object -First 1)
    if ($Installed) { $Installed = $Installed.ToString().Trim() }
  } catch {}
}
if ($Installed -and ($Installed -match [regex]::Escape($Tag) -or $Installed -match [regex]::Escape($Ver))) {
  Write-Host "OK $App already at $Tag — skipping (currently: $Installed)"
  Start-Agent
  exit 0
}

$Url = "https://github.com/$Repo/releases/download/$Tag/$App-$Ver-windows-$Arch.msi"
$Tmp = [IO.Path]::GetTempFileName() + '.msi'
Write-Host "-> $Url"
if ($Installed) { Write-Host "   (upgrading from $Installed)" }
Invoke-WebRequest -Headers $Headers $Url -OutFile $Tmp
Start-Process msiexec -ArgumentList "/i `"$Tmp`" /qn" -Wait
Remove-Item $Tmp
Write-Host "OK $App installed"
Start-Agent
