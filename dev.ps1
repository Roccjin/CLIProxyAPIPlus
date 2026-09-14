# One-click local debug for CLIProxyAPIPlus + Management Center.
# Usage:
#   pwsh -File .\dev.ps1
#   .\dev.cmd
#   .\dev.ps1 -BackendOnly
#   .\dev.ps1 -FrontendOnly
#   .\dev.ps1 -Stop
# -BackendOnly and -FrontendOnly cannot be combined.
# Ctrl+C / normal exit only stops processes this script started.
# Use -Stop to kill listeners on the UI and backend ports.

[CmdletBinding()]
param(
    [switch]$BackendOnly,
    [switch]$FrontendOnly,
    [switch]$NoBrowser,
    [switch]$FetchModels,
    [switch]$Stop,
    [string]$UiDir = '',
    [string]$ManagementKey = 'local-debug',
    [string]$ApiKey = 'local-debug-key',
    [int]$ReadyTimeoutSec = 180
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
$OutputEncoding = [Console]::OutputEncoding

$Root = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($Root)) {
    $Root = (Get-Location).Path
}

$ConfigPath = Join-Path $Root 'config.yaml'
$ExamplePath = Join-Path $Root 'config.example.yaml'
$DefaultUiDir = Join-Path (Split-Path -Parent $Root) 'Cli-Proxy-API-Management-Center'
$UiPort = 5173
$script:ChildProcesses = @()
$script:CreatedConfig = $false

function Write-Step {
    param([string]$Message)
    Write-Host "[dev] $Message" -ForegroundColor Cyan
}

function Write-WarnStep {
    param([string]$Message)
    Write-Host "[dev] $Message" -ForegroundColor Yellow
}

function Get-ListeningPids {
    param([int]$Port)
    $pids = @()
    try {
        $pids = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
            Select-Object -ExpandProperty OwningProcess -Unique)
    } catch {
        $pids = @()
    }
    if ($pids.Count -eq 0) {
        $lines = & netstat.exe -ano -p tcp
        foreach ($line in $lines) {
            if ($line -notmatch "[:\.]${Port}\s+.+LISTENING\s+(\d+)\s*$") {
                continue
            }
            $pids += [int]$Matches[1]
        }
        $pids = @($pids | Select-Object -Unique)
    }
    return @($pids | Where-Object { $_ -gt 0 })
}

function Test-PortOpen {
    param(
        [string]$TargetHost,
        [int]$Port
    )
    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $async = $client.BeginConnect($TargetHost, $Port, $null, $null)
        $ok = $async.AsyncWaitHandle.WaitOne(400, $false)
        if (-not $ok) {
            return $false
        }
        $client.EndConnect($async)
        return $true
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

function Wait-PortOpen {
    param(
        [string]$Name,
        [string]$TargetHost,
        [int]$Port,
        [int]$TimeoutSec
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (Test-PortOpen -TargetHost $TargetHost -Port $Port) {
            Write-Step "$Name is ready on ${TargetHost}:${Port}"
            return
        }
        Start-Sleep -Milliseconds 400
    }
    throw "$Name did not listen on ${TargetHost}:${Port} within ${TimeoutSec}s"
}

function Stop-PortTree {
    param([int]$Port)
    $pids = Get-ListeningPids -Port $Port
    foreach ($procId in $pids) {
        Write-Step "Stopping PID $procId on port $Port"
        & taskkill.exe /PID $procId /T /F 2>$null | Out-Null
    }
}

function Stop-DevChildren {
    foreach ($proc in $script:ChildProcesses) {
        if ($null -eq $proc) {
            continue
        }
        try {
            if (-not $proc.HasExited) {
                Write-Step "Stopping child PID $($proc.Id)"
                & taskkill.exe /PID $proc.Id /T /F 2>$null | Out-Null
            }
        } catch {
        }
    }
    $script:ChildProcesses = @()
}

function Stop-DevStack {
    Stop-PortTree -Port $UiPort
    $backendPort = 8317
    if (Test-Path -LiteralPath $ConfigPath) {
        $backendPort = Get-ConfigPort -Path $ConfigPath
    }
    Stop-PortTree -Port $backendPort
    Stop-DevChildren
}

function Get-ConfigPort {
    param([string]$Path)
    $line = Select-String -LiteralPath $Path -Pattern '^(?!\s*#)\s*port:\s*(\d+)\s*$' | Select-Object -First 1
    if ($null -eq $line) {
        return 8317
    }
    return [int]$line.Matches.Groups[1].Value
}

function Get-ConfigSecretKey {
    param([string]$Path)
    $line = Select-String -LiteralPath $Path -Pattern '^(?!\s*#)\s*secret-key:\s*(.*)$' | Select-Object -First 1
    if ($null -eq $line) {
        return ''
    }
    $raw = $line.Matches.Groups[1].Value.Trim()
    if ($raw.StartsWith("'") -and $raw.EndsWith("'") -and $raw.Length -ge 2) {
        return $raw.Substring(1, $raw.Length - 2)
    }
    if ($raw.StartsWith('"') -and $raw.EndsWith('"') -and $raw.Length -ge 2) {
        return $raw.Substring(1, $raw.Length - 2)
    }
    return $raw
}

function Test-HasTemplateApiKeys {
    param([string]$Path)
    $text = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    return [regex]::IsMatch($text, "(?m)^[^#\r\n]*your-api-key-[123]")
}

function Initialize-DevConfig {
    if (Test-Path -LiteralPath $ConfigPath) {
        Write-Step "Using existing config.yaml"
        return
    }
    if (-not (Test-Path -LiteralPath $ExamplePath)) {
        throw "Missing config.example.yaml in $Root"
    }

    Write-Step "Creating config.yaml from config.example.yaml"
    $text = Get-Content -LiteralPath $ExamplePath -Raw -Encoding UTF8
    $text = [regex]::Replace($text, "(?m)^host: ''", "host: '127.0.0.1'")
    $text = [regex]::Replace($text, "(?m)^  secret-key: ''", "  secret-key: '$ManagementKey'")
    $text = [regex]::Replace(
        $text,
        "(?m)^  - 'your-api-key-1'\r?\n  - 'your-api-key-2'\r?\n  - 'your-api-key-3'",
        "  - '$ApiKey'"
    )
    $text = [regex]::Replace($text, "(?m)^debug: false", "debug: true")
    $text = [regex]::Replace($text, "(?m)^auth-dir: '~/.cli-proxy-api'", "auth-dir: './auths'")
    Set-Content -LiteralPath $ConfigPath -Value $text -Encoding UTF8
    $script:CreatedConfig = $true
}

function Resolve-UiDir {
    $candidates = @()
    if (-not [string]::IsNullOrWhiteSpace($UiDir)) {
        $candidates += $UiDir
    }
    if (-not [string]::IsNullOrWhiteSpace($env:CPA_UI_DIR)) {
        $candidates += $env:CPA_UI_DIR
    }
    $candidates += $DefaultUiDir
    foreach ($candidate in $candidates) {
        $packageJson = Join-Path $candidate 'package.json'
        if (Test-Path -LiteralPath $packageJson) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    throw "Management Center not found. Set -UiDir or CPA_UI_DIR. Tried: $($candidates -join ', ')"
}

function Assert-Command {
    param(
        [string]$Name,
        [string]$Hint
    )
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Missing command '$Name'. $Hint"
    }
}

function Get-UiInstallCommand {
    $viteArgs = "--host 127.0.0.1 --port $UiPort --strictPort"
    if (Get-Command bun -ErrorAction SilentlyContinue) {
        return @{
            Manager = 'bun'
            Install = @('install', '--frozen-lockfile')
            DevLine = "bun run dev -- $viteArgs"
        }
    }
    Write-WarnStep "bun not found; falling back to npm for the Management Center"
    return @{
        Manager = 'npm'
        Install = @('install')
        DevLine = "npm run dev -- $viteArgs"
    }
}

function Install-UiDependencies {
    param(
        [string]$Path,
        [hashtable]$Runner
    )
    $nodeModules = Join-Path $Path 'node_modules'
    if (Test-Path -LiteralPath $nodeModules) {
        return
    }
    Write-Step "Installing Management Center dependencies with $($Runner.Manager)"
    Push-Location -LiteralPath $Path
    try {
        & $Runner.Manager @($Runner.Install)
        if ($LASTEXITCODE -ne 0) {
            throw "$($Runner.Manager) install failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
}

function Start-DevWindow {
    param(
        [Parameter(Mandatory = $true)][string]$Title,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string]$Command,
        [hashtable]$ExtraEnvironment
    )
    $restore = @{}
    $assign = @{
        CPA_DEV_WINDOW_TITLE = $Title
    }
    if ($null -ne $ExtraEnvironment) {
        foreach ($key in @($ExtraEnvironment.Keys)) {
            $assign[$key] = [string]$ExtraEnvironment[$key]
        }
    }
    foreach ($key in @($assign.Keys)) {
        $restore[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $assign[$key], 'Process')
    }
    try {
        $script = @"
`$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
if (-not [string]::IsNullOrWhiteSpace(`$env:CPA_DEV_WINDOW_TITLE)) {
    `$Host.UI.RawUI.WindowTitle = `$env:CPA_DEV_WINDOW_TITLE
}
$Command
"@
        $encoded = [Convert]::ToBase64String([System.Text.Encoding]::Unicode.GetBytes($script))
        $proc = Start-Process -FilePath 'pwsh' -WorkingDirectory $WorkingDirectory -PassThru -ArgumentList @(
            '-NoProfile',
            '-NoExit',
            '-EncodedCommand',
            $encoded
        )
        $script:ChildProcesses += $proc
        return $proc
    } finally {
        foreach ($key in @($restore.Keys)) {
            [Environment]::SetEnvironmentVariable($key, $restore[$key], 'Process')
        }
    }
}

if ($Stop) {
    Write-Step "Stopping local debug processes"
    Stop-DevStack
    Write-Step "Stopped"
    return
}

if ($BackendOnly -and $FrontendOnly) {
    throw "Use only one of -BackendOnly or -FrontendOnly."
}

Assert-Command -Name 'pwsh' -Hint 'Install PowerShell 7+.'
if (-not $FrontendOnly) {
    Assert-Command -Name 'go' -Hint 'Install Go 1.26+ and ensure it is on PATH.'
}

$authDir = Join-Path $Root 'auths'
if (-not (Test-Path -LiteralPath $authDir)) {
    New-Item -ItemType Directory -Path $authDir | Out-Null
}

if (-not $FrontendOnly) {
    Initialize-DevConfig
}

$backendPort = 8317
$secretKey = ''
$useEnvManagementPassword = $false
$loginKeyDisplay = $ManagementKey
if (Test-Path -LiteralPath $ConfigPath) {
    $backendPort = Get-ConfigPort -Path $ConfigPath
    $secretKey = Get-ConfigSecretKey -Path $ConfigPath
    if ([string]::IsNullOrWhiteSpace($secretKey)) {
        $useEnvManagementPassword = $true
        Write-WarnStep "config.yaml secret-key is empty; this session uses MANAGEMENT_PASSWORD=$ManagementKey"
    } elseif ($secretKey -match '^\$2[aby]\$') {
        $loginKeyDisplay = "(hashed in config.yaml; if created by this script it is '$ManagementKey')"
        Write-WarnStep "config.yaml secret-key is hashed; log in with the original plaintext management key"
    } else {
        $ManagementKey = $secretKey
        $loginKeyDisplay = $secretKey
    }
    if (Test-HasTemplateApiKeys -Path $ConfigPath) {
        Write-WarnStep "config.yaml still has template api-keys (your-api-key-*); /v1 is in safe mode until you change them"
    }
}

if (-not $FrontendOnly) {
    $existingBackend = Get-ListeningPids -Port $backendPort
    if ($existingBackend.Count -gt 0) {
        throw "Port $backendPort is already in use (PID $($existingBackend -join ', ')). Run .\dev.ps1 -Stop first."
    }
}

$resolvedUiDir = $null
$uiRunner = $null
if (-not $BackendOnly) {
    $resolvedUiDir = Resolve-UiDir
    $uiRunner = Get-UiInstallCommand
    Assert-Command -Name $uiRunner.Manager -Hint "Install $($uiRunner.Manager) or bun."
    Install-UiDependencies -Path $resolvedUiDir -Runner $uiRunner
    $existingUi = Get-ListeningPids -Port $UiPort
    if ($existingUi.Count -gt 0) {
        throw "Port $UiPort is already in use (PID $($existingUi -join ', ')). Run .\dev.ps1 -Stop first."
    }
}

$localModelFlag = ''
if (-not $FetchModels) {
    $localModelFlag = ' --local-model'
}

try {
    if (-not $FrontendOnly) {
        Write-Step "Starting CLIProxyAPIPlus on 127.0.0.1:$backendPort"
        $backendEnv = $null
        if ($useEnvManagementPassword) {
            $backendEnv = @{ MANAGEMENT_PASSWORD = $ManagementKey }
        }
        $backendCommand = @"
Write-Host 'go run ./cmd/server --config config.yaml$localModelFlag' -ForegroundColor Green
go run ./cmd/server --config config.yaml$localModelFlag
"@
        [void](Start-DevWindow -Title 'CLIProxyAPIPlus' -WorkingDirectory $Root -Command $backendCommand -ExtraEnvironment $backendEnv)
        Wait-PortOpen -Name 'CLIProxyAPIPlus' -TargetHost '127.0.0.1' -Port $backendPort -TimeoutSec $ReadyTimeoutSec
    }

    if (-not $BackendOnly) {
        Write-Step "Starting Management Center on http://127.0.0.1:$UiPort"
        $frontendCommand = @"
Write-Host '$($uiRunner.DevLine)' -ForegroundColor Green
$($uiRunner.DevLine)
"@
        [void](Start-DevWindow -Title 'Management Center' -WorkingDirectory $resolvedUiDir -Command $frontendCommand -ExtraEnvironment @{
            VITE_CPA_PROXY_TARGET = "http://127.0.0.1:$backendPort"
        })
        Wait-PortOpen -Name 'Management Center' -TargetHost '127.0.0.1' -Port $UiPort -TimeoutSec $ReadyTimeoutSec
    }

    $uiUrl = "http://localhost:$UiPort"
    $backendUrl = "http://127.0.0.1:$backendPort"
    Write-Host ""
    Write-Host "Local debug is running." -ForegroundColor Green
    if (-not $BackendOnly) {
        Write-Host "  Management UI (Vite): $uiUrl"
        Write-Host "  API address in the login form: $uiUrl"
    }
    Write-Host "  Bundled panel:        $backendUrl/management.html"
    Write-Host "  Proxy API:            $backendUrl/v1"
    Write-Host "  Management key:       $loginKeyDisplay"
    if ($script:CreatedConfig) {
        Write-Host "  Proxy API key:        $ApiKey"
    } elseif ((Test-Path -LiteralPath $ConfigPath) -and -not (Test-HasTemplateApiKeys -Path $ConfigPath)) {
        Write-Host "  Proxy API key:        use the api-keys value in config.yaml"
    }
    Write-Host "  Stop:                 .\dev.ps1 -Stop"
    Write-Host ""
    Write-Host "Leave API address as the Vite origin ($uiUrl) so /v0 is proxied. Do not point the UI at :$backendPort (no CORS)." -ForegroundColor DarkGray

    if (-not $NoBrowser) {
        if (-not $BackendOnly) {
            Start-Process $uiUrl | Out-Null
        } else {
            Start-Process "$backendUrl/management.html" | Out-Null
        }
    }

    Write-Host "Press Ctrl+C to stop processes started by this script." -ForegroundColor Yellow
    while ($true) {
        $alive = @($script:ChildProcesses | Where-Object { $_ -and -not $_.HasExited })
        if ($alive.Count -eq 0) {
            break
        }
        Start-Sleep -Seconds 1
    }
} finally {
    Write-Step "Shutting down"
    Stop-DevChildren
}
