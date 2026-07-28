[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$RepositoryRoot,

    [Parameter(Mandatory = $false)]
    [ValidateRange(1, 300)]
    [int]$ShutdownTimeoutSeconds = 10
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $RepositoryRoot -PathType Container)) {
    throw "仓库目录不存在或不可访问：$RepositoryRoot"
}

function Get-ConfiguredPorts {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ConfigPath
    )

    $ports = @{
        backendListenAddr = 18090
        proxyListenAddr   = 18080
    }

    if (-not (Test-Path -LiteralPath $ConfigPath -PathType Leaf)) {
        return @($ports.Values | Sort-Object -Unique)
    }

    $content = Get-Content -LiteralPath $ConfigPath -Raw
    foreach ($key in @("backendListenAddr", "proxyListenAddr")) {
        $pattern = '(?m)^\s*{0}\s*:\s*(?<address>[^#\r\n]+)\s*(?:#.*)?$' -f [regex]::Escape($key)
        $match = [regex]::Match($content, $pattern)
        if (-not $match.Success) {
            continue
        }

        $address = $match.Groups["address"].Value.Trim().Trim([char]34, [char]39)
        $portMatch = [regex]::Match($address, ":(?<port>\d+)\s*$")
        if (-not $portMatch.Success) {
            throw "无法从配置项 $key 解析监听端口：$address"
        }

        $port = [int]$portMatch.Groups["port"].Value
        if ($port -lt 1 -or $port -gt 65535) {
            throw "配置项 $key 的监听端口无效：$port"
        }
        $ports[$key] = $port
    }

    return @($ports.Values | Sort-Object -Unique)
}

function Get-TargetListeners {
    param(
        [Parameter(Mandatory = $true)]
        [int[]]$Ports
    )

    return @(
        Get-NetTCPConnection -State Listen -ErrorAction Stop |
            Where-Object { $Ports -contains [int]$_.LocalPort }
    )
}

function Get-ProcessExecutablePath {
    param(
        [Parameter(Mandatory = $true)]
        [int]$ProcessId
    )

    $processInfo = Get-CimInstance -ClassName Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction Stop
    if ($null -eq $processInfo -or [string]::IsNullOrWhiteSpace($processInfo.ExecutablePath)) {
        throw "无法确认 PID $ProcessId 的可执行文件路径，拒绝自动终止。"
    }
    return [string]$processInfo.ExecutablePath
}

$repositoryPath = [System.IO.Path]::GetFullPath($RepositoryRoot).TrimEnd([char[]]@('\', '/'))
$binPath = [System.IO.Path]::GetFullPath((Join-Path $repositoryPath "bin")).TrimEnd([char[]]@('\', '/'))
$configPath = Join-Path $HOME ".cursor-local-assistant-v2\config.yaml"
$ports = @(Get-ConfiguredPorts -ConfigPath $configPath)
$listeners = @(Get-TargetListeners -Ports $ports)

if ($listeners.Count -eq 0) {
    Write-Host "开发启动检查：端口 $($ports -join ', ') 未被旧实例占用。"
    exit 0
}

$ownerIds = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique)
if ($ownerIds.Count -ne 1) {
    $owners = $listeners |
        Sort-Object LocalPort |
        ForEach-Object { "$($_.LocalAddress):$($_.LocalPort) -> PID $($_.OwningProcess)" }
    throw "开发端口由不同进程占用，拒绝自动终止：$($owners -join '; ')"
}

$processId = [int]$ownerIds[0]
$ownedPorts = @(
    $listeners |
        Where-Object { [int]$_.OwningProcess -eq $processId } |
        Select-Object -ExpandProperty LocalPort -Unique
)
$missingPorts = @($ports | Where-Object { $ownedPorts -notcontains $_ })
if ($missingPorts.Count -gt 0) {
    throw "PID $processId 未同时占用全部开发端口，拒绝自动终止。缺少端口：$($missingPorts -join ', ')"
}

$executablePath = [System.IO.Path]::GetFullPath(
    (Get-ProcessExecutablePath -ProcessId $processId)
)
$expectedPrefix = $binPath + [System.IO.Path]::DirectorySeparatorChar
$isRepositoryBinary = $executablePath.StartsWith(
    $expectedPrefix,
    [System.StringComparison]::OrdinalIgnoreCase
)
$isExpectedName = [System.IO.Path]::GetFileName($executablePath).Equals(
    "Cursor助手.exe",
    [System.StringComparison]::OrdinalIgnoreCase
)
if (-not $isRepositoryBinary -or -not $isExpectedName) {
    throw "开发端口被非本仓库实例占用，拒绝自动终止：PID $processId，路径 $executablePath"
}

Write-Host "开发启动检查：正在停止旧实例 PID $processId（$executablePath）。"
$process = Get-Process -Id $processId -ErrorAction Stop
$null = $process.CloseMainWindow()

$gracefulDeadline = [DateTime]::UtcNow.AddSeconds(2)
while (-not $process.HasExited -and [DateTime]::UtcNow -lt $gracefulDeadline) {
    Start-Sleep -Milliseconds 100
    $process.Refresh()
}
if (-not $process.HasExited) {
    Stop-Process -Id $processId -Force -ErrorAction Stop
}

$releaseDeadline = [DateTime]::UtcNow.AddSeconds($ShutdownTimeoutSeconds)
do {
    Start-Sleep -Milliseconds 100
    $remainingListeners = @(Get-TargetListeners -Ports $ports)
} while ($remainingListeners.Count -gt 0 -and [DateTime]::UtcNow -lt $releaseDeadline)

if ($remainingListeners.Count -gt 0) {
    $remaining = $remainingListeners |
        Sort-Object LocalPort |
        ForEach-Object { "$($_.LocalAddress):$($_.LocalPort) -> PID $($_.OwningProcess)" }
    throw "旧实例停止后开发端口仍未释放：$($remaining -join '; ')"
}

Write-Host "dev preflight complete"
