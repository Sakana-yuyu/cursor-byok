[CmdletBinding(SupportsShouldProcess)]
param(
    [switch]$Check,
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$source = (Resolve-Path (Join-Path $PSScriptRoot "..\skills\plan-docs")).Path
$target = [System.IO.Path]::GetFullPath((Join-Path $HOME ".cursor\skills\plan-docs"))

if (-not (Test-Path (Join-Path $source "SKILL.md"))) {
    throw "plan-docs source was not found: $source"
}

function Get-SkillHash([string]$path) {
    if (-not (Test-Path $path)) {
        return ""
    }
    $files = Get-ChildItem -LiteralPath $path -File -Recurse | Sort-Object FullName
    $content = foreach ($file in $files) {
        $relative = $file.FullName.Substring($path.Length).TrimStart([char[]]"\/")
        $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
        "$relative`n$hash"
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes(($content -join "`n"))
    $stream = [System.IO.MemoryStream]::new($bytes)
    try {
        return (Get-FileHash -InputStream $stream -Algorithm SHA256).Hash.ToLowerInvariant()
    } finally {
        $stream.Dispose()
    }
}

$sourceHash = Get-SkillHash $source
$targetHash = Get-SkillHash $target

if ($Check) {
    if ($sourceHash -eq $targetHash -and $sourceHash) {
        Write-Output "plan-docs is up to date: $target"
        exit 0
    }
    Write-Output "plan-docs requires sync: source=$sourceHash target=$targetHash"
    exit 1
}

if (Test-Path $target) {
    if ($sourceHash -eq $targetHash) {
        Write-Output "plan-docs is already up to date: $target"
        exit 0
    }
    if (-not $Force) {
        throw "A different plan-docs skill already exists at $target. Re-run with -Force to back it up and replace it."
    }
    $backup = "$target.backup-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
    if ($PSCmdlet.ShouldProcess($target, "Back up to $backup")) {
        Move-Item -LiteralPath $target -Destination $backup
        Write-Output "Backed up existing skill to $backup"
    }
}

$parent = Split-Path -Parent $target
if ($PSCmdlet.ShouldProcess($target, "Install plan-docs skill")) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    Copy-Item -LiteralPath $source -Destination $target -Recurse -Force
}

$installedHash = Get-SkillHash $target
if ($installedHash -ne $sourceHash) {
    throw "plan-docs verification failed after installation"
}

Write-Output "Installed plan-docs to $target"
Write-Output "Restart Cursor or reload the window, then invoke /plan-docs or enter Plan mode with a planning request."