# Universal installer for Windows — downloads the .msi from GitHub
# release and installs silently (per-user, no UAC). Detects arch from
# PROCESSOR_ARCHITECTURE.
#
# Public repo:   iwr -useb <url>/install.ps1 | iex
# Private repo:  $env:TOKEN='ghp_xxx'; iwr -useb -Headers @{Authorization="Bearer $env:TOKEN"} <url>/install.ps1 | iex

$ErrorActionPreference = 'Stop'

$App   = 'wick'                  # auto-rewritten by `wick init`
$Repo  = 'yogasw/wick'           # auto-rewritten by `wick init` — EDIT after init
$Token = if ($env:TOKEN) { $env:TOKEN } else { '' }

$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

$Headers = @{}
if ($Token) { $Headers['Authorization'] = "Bearer $Token" }

$Tag = if ($env:VERSION -and $env:VERSION -ne 'latest') {
  $env:VERSION
} else {
  (Invoke-RestMethod -Headers $Headers "https://api.github.com/repos/$Repo/releases/latest").tag_name
}
if (-not $Tag) { throw "could not resolve latest tag for $Repo" }
$Ver = $Tag.TrimStart('v')
$Url = "https://github.com/$Repo/releases/download/$Tag/$App-$Ver-windows-$Arch.msi"
$Tmp = [IO.Path]::GetTempFileName() + '.msi'
Write-Host "-> $Url"
Invoke-WebRequest -Headers $Headers $Url -OutFile $Tmp
Start-Process msiexec -ArgumentList "/i `"$Tmp`" /qn" -Wait
Remove-Item $Tmp
Write-Host "OK $App installed"
