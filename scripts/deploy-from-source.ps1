# deploy-from-source.ps1 — build wick (the wick-agent app) or your own wick app from source
# on Windows and install it. Installs what the build needs (Git, Go, the wick CLI) when
# missing, per-user, no admin prompt. Linux / macOS: deploy-from-source.sh.
#
#   # wick itself, from branch master (install the first time, update after that)
#   & ([scriptblock]::Create((iwr -useb https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.ps1)))
#   # another branch or tag
#   & ([scriptblock]::Create((iwr -useb https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.ps1))) -Ref develop
#   # your own wick app + its plugins
#   & ([scriptblock]::Create((iwr -useb https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.ps1))) all -Git https://github.com/org/myapp.git
#
# Private repo: $env:TOKEN='ghp_xxx' first (sent as a header, never written to .git/config).
# Windows has no zero-downtime handover: an update stops the app, swaps the .exe, starts it.
#
# Commands: wick (default) | setup | core | plugins | all | status   (guide: /guide/deploy-from-source)
param(
  [Parameter(Position = 0)][string]$Command = 'wick',
  [Parameter(Position = 1, ValueFromRemainingArguments = $true)][string[]]$Keys = @(),
  [string]$Dir = '',
  [string]$Git = '',
  [string]$Branch = '',
  [string]$App = '',
  [string]$Wick = '',
  [string]$Ref = 'master',
  [string]$Src = (Join-Path $env:USERPROFILE 'wick-src'),
  [switch]$NoUpgrade, [switch]$NoPull, [switch]$NoBump, [switch]$NoTest,
  [switch]$AllPlugins, [switch]$BumpPlugins, [switch]$DryRun
)
$ErrorActionPreference = 'Stop'

function Say($m) { Write-Host "`n== $m" }
function Die($m) { Write-Host "!! $m" -ForegroundColor Red; exit 1 }
# Run prints a command and runs it (unless -DryRun); a non-zero exit stops the script.
function Run([string]$Exe, [string[]]$CmdArgs) {
  Write-Host "   > $Exe $($CmdArgs -join ' ')"
  if ($DryRun) { return }
  & $Exe @CmdArgs
  if ($LASTEXITCODE -ne 0) { Die "$Exe exited $LASTEXITCODE" }
}
function Add-UserPath($p) {
  if (($env:Path -split ';') -notcontains $p) { $env:Path = "$p;$env:Path" }
  $user = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not $DryRun -and (($user -split ';') -notcontains $p)) {
    [Environment]::SetEnvironmentVariable('Path', "$p;$user", 'User')
  }
}

$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$Target = "windows/$Arch"
$GoRoot = Join-Path $env:LOCALAPPDATA 'Programs\Go'
if (Test-Path "$GoRoot\bin\go.exe") { Add-UserPath "$GoRoot\bin" }
$GoBin = Join-Path $env:USERPROFILE 'go\bin'
Add-UserPath $GoBin

function GitAuth([string[]]$GitArgs) {
  if ($env:TOKEN) {
    $b64 = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes("x-access-token:$($env:TOKEN)"))
    $GitArgs = @('-c', "http.extraHeader=Authorization: Basic $b64") + $GitArgs
    Write-Host "   > git (token header) $($GitArgs[2..($GitArgs.Count - 1)] -join ' ')"
    if ($DryRun) { return }
    & git @GitArgs; if ($LASTEXITCODE -ne 0) { Die "git exited $LASTEXITCODE" }
  } else { Run git $GitArgs }
}

function Resolve-AppDir {
  if (-not $script:Dir) {
    if (Test-Path wick.yml) { $script:Dir = (Get-Location).Path }
    elseif ($Git) { $script:Dir = Join-Path $env:USERPROFILE ([IO.Path]::GetFileNameWithoutExtension($Git.TrimEnd('/'))) }
    else { Die 'no app here: run inside the app checkout, or pass -Dir / -Git' }
  }
  if (-not (Test-Path (Join-Path $script:Dir '.git'))) {
    if (-not $Git) { Die "$($script:Dir) is not a git checkout (pass -Git URL to clone it)" }
    Say "clone $Git -> $($script:Dir)"
    $a = @('clone'); if ($Branch) { $a += @('--branch', $Branch) }; GitAuth ($a + @($Git, $script:Dir))
    $script:NoPull = $true
  }
  if ($DryRun -and -not (Test-Path $script:Dir)) { return }
  $yml = Join-Path $script:Dir 'wick.yml'
  if (-not (Test-Path $yml)) { Die "$($script:Dir) has no wick.yml — not a wick app" }
  if (-not $script:App) {
    $m = Select-String -Path $yml -Pattern '^name:\s*(\S+)' | Select-Object -First 1
    if ($m) { $script:App = $m.Matches[0].Groups[1].Value } else { Die 'no name: in wick.yml (pass -App)' }
  }
}

function Pull-Repo {
  if ($NoPull) { return }
  Say 'git pull'
  $a = @('-C', $Dir, 'pull', '--ff-only'); if ($Branch) { $a += @('origin', $Branch) }; GitAuth $a
  $script:NoPull = $true
}

function GoWanted($mod) {
  if (Test-Path $mod) {
    $t = Select-String -Path $mod -Pattern '^toolchain go(\S+)' | Select-Object -First 1
    if ($t) { return $t.Matches[0].Groups[1].Value }
    $g = Select-String -Path $mod -Pattern '^go (\d\S*)' | Select-Object -First 1
    if ($g) { return $g.Matches[0].Groups[1].Value }
  }
  return ((Invoke-WebRequest -UseBasicParsing 'https://go.dev/VERSION?m=text').Content -split "`n")[0] -replace '^go', ''
}

function Setup {
  Setup-Git
  Setup-Go (Join-Path $Dir 'go.mod')
  Setup-WickCli
}

function Setup-Git {
  if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) { Die 'install Git first: https://git-scm.com/download/win' }
    Say 'install Git'
    Run winget @('install', '--id', 'Git.Git', '-e', '--silent', '--accept-source-agreements', '--accept-package-agreements')
    $env:Path = "$env:ProgramFiles\Git\cmd;$env:Path"
  }
}

function Setup-Go($mod) {
  $want = GoWanted $mod; $have = ''
  if (Get-Command go -ErrorAction SilentlyContinue) { $have = (& go env GOVERSION) -replace '^go', '' }
  if (-not $have -or ([version]($have -replace '[^\d.].*$', '')) -lt [version]($want -replace '[^\d.].*$', '')) {
    Say "install Go $want (have: $(if ($have) { $have } else { 'none' })) -> $GoRoot"
    $zip = Join-Path $env:TEMP "go$want.windows-$Arch.zip"
    Write-Host "   > download https://go.dev/dl/go$want.windows-$Arch.zip"
    if (-not $DryRun) {
      Invoke-WebRequest -UseBasicParsing "https://go.dev/dl/go$want.windows-$Arch.zip" -OutFile $zip
      if (Test-Path $GoRoot) { Remove-Item -Recurse -Force $GoRoot }
      $parent = Split-Path $GoRoot; New-Item -ItemType Directory -Force $parent | Out-Null
      Expand-Archive $zip -DestinationPath $parent -Force   # the zip holds a top-level go\ folder
      Remove-Item $zip
    }
    Add-UserPath "$GoRoot\bin"
  } else { Write-Host "   Go $have ok (need $want)" }
}

function Setup-WickCli {
  if (-not (Get-Command wick -ErrorAction SilentlyContinue)) {
    $wv = 'latest'; $mod = Join-Path $Dir 'go.mod'
    if (Test-Path $mod) { $m = Select-String -Path $mod -Pattern '^\s*github.com/yogasw/wick (\S+)' | Select-Object -First 1; if ($m) { $wv = $m.Matches[0].Groups[1].Value } }
    Say "install wick CLI $wv"
    Run go @('install', "github.com/yogasw/wick@$wv")
  } else { Write-Host "   wick CLI ok ($((Get-Command wick).Source))" }
}

function Find-AppExe {
  $c = Get-Command $App -ErrorAction SilentlyContinue
  if ($c) { return $c.Source }
  $p = Join-Path $env:LOCALAPPDATA "Programs\$App\$App.exe"
  if (Test-Path $p) { return $p }
  return $null
}

$script:GoModSaved = $null
function Use-LocalWick {
  if (-not $Wick) { return }
  if (-not (Test-Path (Join-Path $Wick 'go.mod'))) { Die "-Wick $Wick is not a wick checkout" }
  $script:GoModSaved = @{ mod = Get-Content -Raw go.mod; sum = (Get-Content -Raw go.sum -ErrorAction SilentlyContinue) }
  Run go @('mod', 'edit', '-replace', "github.com/yogasw/wick=$((Resolve-Path $Wick).Path)")
}
function Restore-GoMod {
  if (-not $script:GoModSaved) { return }
  Set-Content -NoNewline go.mod $script:GoModSaved.mod
  if ($script:GoModSaved.sum) { Set-Content -NoNewline go.sum $script:GoModSaved.sum }
  $script:GoModSaved = $null
}

function Core {
  if ($DryRun -and -not (Test-Path $Dir)) { Write-Host "   (dry-run: $Dir is not cloned yet, nothing more to show)"; return }
  Push-Location $Dir
  try {
    Pull-Repo
    $exe = Find-AppExe
    $yml = Get-Content -Raw wick.yml
    $cur = [regex]::Match($yml, '(?m)^version:\s*(\S+)').Groups[1].Value
    if (-not $cur) { Die 'no version: in wick.yml' }
    if (-not $NoBump -and $exe) {
      $p = $cur -split '\.'; $p[-1] = [int]$p[-1] + 1; $new = $p -join '.'
      Say "version $cur -> $new"
      if (-not $DryRun) { Set-Content -NoNewline wick.yml ([regex]::new('(?m)^version:.*$').Replace($yml, "version: $new", 1)) }
    }
    Say "wick source: $(if ($Wick) { $Wick } else { 'release (go.mod)' })"
    if ($Wick) { Use-LocalWick }
    elseif (Select-String -Path go.mod -Pattern '^replace github.com/yogasw/wick' -Quiet) { Die 'go.mod has a replace for wick: pass -Wick DIR, or drop it: go mod edit -dropreplace github.com/yogasw/wick' }
    elseif (-not $NoUpgrade) { Run wick @('upgrade', '-y') }
    Get-ChildItem bin -Filter "$App-windows-*" -ErrorAction SilentlyContinue | Remove-Item -Force
    if (-not $NoTest) { Say 'test'; Run go @('test', '-p', '1', './...') }
    Say "build ($Target)"; Run wick @('build', '--target', $Target)
    Restore-GoMod
    Install-Build $exe (Join-Path $Dir "bin\$App-windows-$Arch.exe")
  } finally { Restore-GoMod; Pop-Location }
}

# Install-Build: a first install into %LOCALAPPDATA%\Programs\<app> (where install.ps1 puts
# a release too), else stop, swap the .exe, start. Windows has no socket handover.
function Install-Build($exe, $bin) {
    if (-not $DryRun -and -not (Test-Path $bin)) { Die "build produced no $bin" }
    if (-not $exe) {
      $dest = Join-Path $env:LOCALAPPDATA "Programs\$App"
      Say "first install -> $dest\$App.exe"
      if (-not $DryRun) { New-Item -ItemType Directory -Force $dest | Out-Null; Copy-Item $bin "$dest\$App.exe" -Force }
      Add-UserPath $dest
      Run "$dest\$App.exe" @('start')
      return
    }
    Say "stop, swap, start ($exe)"
    Write-Host "   > $exe stop"
    if (-not $DryRun) {
      & $exe stop 2>$null
      for ($i = 0; $i -lt 30 -and (Get-Process -Name $App -ErrorAction SilentlyContinue); $i++) { Start-Sleep 1 }
      Copy-Item $bin $exe -Force
    }
    Run $exe @('start')
    Say 'now running'; Run $exe @('version')
}

# Wick-Self builds wick itself at -Ref: the wick CLI from the source, then the wick-agent app
# (what the release ships: `wick init wick-agent`), scaffolded once and built against it.
function Wick-Self {
  if (-not $script:Dir) { $script:Dir = Join-Path $env:USERPROFILE 'wick-agent' }
  if (-not $script:App) { $script:App = Split-Path $script:Dir -Leaf }
  Setup-Git
  if (Test-Path (Join-Path $Src '.git')) {
    Say "wick source $Src -> $Ref"
    Run git @('-C', $Src, 'fetch', '--quiet', 'origin', $Ref)
    Run git @('-C', $Src, 'checkout', '--quiet', '--detach', 'FETCH_HEAD')
  } else {
    Say "clone wick ($Ref) -> $Src"
    Run git @('clone', '--quiet', '--branch', $Ref, 'https://github.com/yogasw/wick.git', $Src)
  }
  Setup-Go (Join-Path $Src 'go.mod')
  Say 'wick CLI from source'
  if (-not $DryRun) { Push-Location $Src; try { $env:GOWORK = 'off'; Run go @('install', '.') } finally { Remove-Item Env:GOWORK -ErrorAction SilentlyContinue; Pop-Location } }
  else { Write-Host "   > (cd $Src) go install ." }
  $exe = Find-AppExe
  if (-not (Test-Path (Join-Path $Dir 'wick.yml'))) {
    Say "scaffold $Dir"
    $parent = Split-Path $Dir
    if (-not $DryRun) { New-Item -ItemType Directory -Force $parent | Out-Null; Push-Location $parent; try { Run wick @('init', (Split-Path $Dir -Leaf), '--skip-setup') } finally { Pop-Location } }
    else { Write-Host "   > (cd $parent) wick init $(Split-Path $Dir -Leaf) --skip-setup"; Write-Host '   (dry-run: not scaffolded yet, nothing more to show)'; return }
  }
  Push-Location $Dir
  try {
    Run go @('mod', 'edit', '-replace', "github.com/yogasw/wick=$((Resolve-Path $Src).Path)")
    Say 'setup (tailwind, templ, go mod tidy)'; Run wick @('setup')
    # the source's VERSION, next patch, plus the build time as a 4th number: newer than the
    # release it came from and than the previous source build (versions compare numerically)
    $base = '0.0.0'; $vf = Join-Path $Src 'VERSION'; if (Test-Path $vf) { $base = (Get-Content $vf -Raw).Trim().TrimStart('v') }
    $p = $base -split '\.'; $ver = "$($p[0]).$($p[1]).$([int]$p[2] + 1).$(Get-Date -Format yyyyMMddHHmm)"
    Say "version $ver"
    if (-not $DryRun) { $yml = Get-Content -Raw wick.yml; Set-Content -NoNewline wick.yml ([regex]::new('(?m)^version:.*$').Replace($yml, "version: $ver", 1)) }
    Get-ChildItem bin -Filter "$App-windows-*" -ErrorAction SilentlyContinue | Remove-Item -Force
    Say "build ($Target)"; Run wick @('build', '--target', $Target)
    Install-Build $exe (Join-Path $Dir "bin\$App-windows-$Arch.exe")
  } finally { Pop-Location }
}

function Installed-Version($key, $kind, $exe) {
  foreach ($l in (& $exe plugin list 2>$null)) {
    $f = -split $l
    if ($f.Count -ge 3 -and $f[0] -eq $key -and $f[1] -eq $kind) { return $f[2] }
  }
  return ''
}

function Plugins {
  if ($DryRun -and -not (Test-Path $Dir)) { Write-Host "   (dry-run: $Dir is not cloned yet, nothing more to show)"; return }
  Push-Location $Dir
  try {
    Pull-Repo
    $exe = Find-AppExe
    if (-not $exe -and -not $DryRun) { Die "$App is not installed — run core first" }
    $todo = @()
    foreach ($kind in 'connector', 'tool', 'job', 'service') {
      foreach ($d in (Get-ChildItem "plugins\$kind" -Directory -ErrorAction SilentlyContinue)) {
        if ($d.Name -eq '_template' -or -not (Test-Path "$($d.FullName)\VERSION")) { continue }
        $ver = (Get-Content "$($d.FullName)\VERSION" -Raw).Trim()
        if ($Keys.Count -gt 0) { if ($Keys -notcontains $d.Name) { continue } }
        elseif (-not $AllPlugins -and $exe) {
          $have = Installed-Version $d.Name $kind $exe
          # never downgrade a plugin installed from another source
          if ($have -and [version]$ver -le [version]$have) { continue }
        }
        $todo += , @($kind, $d.Name)
      }
    }
    if ($todo.Count -eq 0) { Say 'plugins: nothing newer than what is installed (-AllPlugins or name keys to force)'; return }
    Say "plugins: $(($todo | ForEach-Object { $_ -join '/' }) -join ' ')"
    Use-LocalWick
    foreach ($t in $todo) {
      $kind, $key = $t
      $vf = "plugins\$kind\$key\VERSION"
      if ($BumpPlugins -and -not $DryRun) {
        $p = (Get-Content $vf -Raw).Trim() -split '\.'; $p[-1] = [int]$p[-1] + 1; Set-Content $vf ($p -join '.')
      }
      $ver = (Get-Content $vf -Raw).Trim()
      if (-not $NoTest) { Run go @('test', "./plugins/$kind/$key/...") }
      Push-Location plugins
      try { $env:GOWORK = 'off'; Run wick @('plugin', 'build', '--kind', $kind, $key, '--target', $Target) }
      finally { Remove-Item Env:GOWORK -ErrorAction SilentlyContinue; Pop-Location }
      $zip = "plugins\bin\$key-$ver-windows-$Arch.zip"
      if (-not $DryRun -and -not (Test-Path $zip)) { Die "build produced no $zip" }
      Run $(if ($exe) { $exe } else { $App }) @('plugin', 'install', $zip)
      if (-not $DryRun) {
        for ($i = 0; $i -lt 15 -and (Installed-Version $key $kind $exe) -ne $ver; $i++) { Start-Sleep 2 }
        if ((Installed-Version $key $kind $exe) -ne $ver) { Die "$key installed but plugin list does not show $ver" }
        Write-Host "   ok $kind/$key $ver"; Remove-Item $zip
      }
    }
  } finally { Restore-GoMod; Pop-Location }
}

switch ($Command) {
  'wick'    { Wick-Self }
  'setup'   { Resolve-AppDir; Setup }
  'core'    { Resolve-AppDir; Core }
  'plugins' { Resolve-AppDir; Plugins }
  'all'     { Resolve-AppDir; Setup; Core; Plugins }
  'status'  { Resolve-AppDir; $e = Find-AppExe; if (-not $e) { Die "$App is not installed" }; & $e version; & $e plugin list }
  default   { Get-Content $PSCommandPath -TotalCount 11 -ErrorAction SilentlyContinue; Write-Host 'Usage: deploy-from-source.ps1 [wick|setup|core|plugins|all|status] [-Ref BRANCH] [-Src DIR] [-Git URL] [-Dir DIR] [-Branch B] [-App NAME] [-Wick DIR] [-NoUpgrade] [-NoPull] [-NoBump] [-NoTest] [-AllPlugins] [-BumpPlugins] [-DryRun] [plugin-key ...]'; exit 2 }
}
