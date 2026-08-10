<#
.SYNOPSIS
    Reproducible verification entry for Cursor proto extraction.
    Runs on Windows PowerShell without Bash dependency.
.DESCRIPTION
    Copies Cursor JS source, validates SHA256, performs two default-strict
    extractions, requires zero extraction errors, parses all generated protos,
    compares sorted byte hashes, and compares normalized generated files to
    checked-in proto/from_extensions definitions.
.PARAMETER CursorJsPath
    Path to the installed Cursor main.js bundle
    (e.g., D:\cursor\resources\app\extensions\cursor-always-local\dist\main.js)
.PARAMETER ExpectedSha256
    Expected SHA256 hash of the source file (uppercase hex).
    Default: 17C57A32DE56399C119DD8FEE7733A8BB86E74A4EC798B318A7926A721CE1963
.EXAMPLE
    .\verify-extraction.ps1 -CursorJsPath "D:\cursor\resources\app\extensions\cursor-always-local\dist\main.js"
#>

param(
    [string]$CursorJsPath,

    [string]$ExpectedSha256 = "17C57A32DE56399C119DD8FEE7733A8BB86E74A4EC798B318A7926A721CE1963",

    [switch]$SelfTestComparison
)

$ErrorActionPreference = "Stop"

function Test-OrdinalEqual {
    param([AllowEmptyString()][string]$Left, [AllowEmptyString()][string]$Right)
    return [string]::Equals($Left, $Right, [StringComparison]::Ordinal)
}

if ($SelfTestComparison) {
    if (-not (Test-OrdinalEqual "AgentClientMessage" "AgentClientMessage")) {
        Write-Error "Ordinal comparison rejected identical content"
        exit 1
    }
    if (Test-OrdinalEqual "AgentClientMessage" "agentClientMessage") {
        Write-Error "Ordinal comparison accepted a case-only mutation"
        exit 1
    }
    Write-Host "Case-sensitive comparison self-test: PASS" -ForegroundColor Green
    exit 0
}

if ([string]::IsNullOrWhiteSpace($CursorJsPath)) {
    Write-Error "CursorJsPath is required unless -SelfTestComparison is used"
    exit 1
}

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Resolve-Path "$ScriptDir\..\..\.."
$ExtToolDir = "$RepoRoot\proto\ext_tool"
$FromExtensionsDir = "$RepoRoot\proto\from_extensions"
$AnalysisRoot = "$RepoRoot\.analysis-tmp"
$AnalysisTmp = "$AnalysisRoot\proto-extraction-verifier"
$OutputDir1 = "$AnalysisTmp\run1"
$OutputDir2 = "$AnalysisTmp\run2"
$SourceCopy = "$AnalysisTmp\main.js"

Write-Host "=== Cursor Proto Extraction Verification ===" -ForegroundColor Cyan
Write-Host "Cursor JS: $CursorJsPath"
Write-Host "Repo root: $RepoRoot"
Write-Host ""

# ---- Validate source exists ----
if (-not (Test-Path $CursorJsPath)) {
    Write-Error "Cursor JS file not found: $CursorJsPath"
    exit 1
}

# ---- Prepare dedicated verifier directory ----
if (Test-Path $AnalysisTmp) {
    Write-Host "Cleaning existing proto extraction verifier directory..."
    Remove-Item -Recurse -Force $AnalysisTmp
}
New-Item -ItemType Directory -Force -Path $AnalysisRoot | Out-Null
New-Item -ItemType Directory -Force -Path $AnalysisTmp | Out-Null
New-Item -ItemType Directory -Force -Path $OutputDir1 | Out-Null
New-Item -ItemType Directory -Force -Path $OutputDir2 | Out-Null

# ---- Copy source ----
Write-Host "Copying source to .analysis-tmp..."
Copy-Item $CursorJsPath $SourceCopy

# ---- Validate SHA256 ----
Write-Host "Validating SHA256..."
$actualHash = (Get-FileHash -Path $SourceCopy -Algorithm SHA256).Hash
Write-Host "  Expected: $ExpectedSha256"
Write-Host "  Actual:   $actualHash"
if ($actualHash -cne $ExpectedSha256) {
    Write-Error "SHA256 mismatch: expected $ExpectedSha256, got $actualHash"
    exit 1
}

# ---- Build extractor ----
Write-Host "Building extractor..."
$previousGoCache = $env:GOCACHE
$env:GOCACHE = "$AnalysisTmp\go-cache"
Push-Location $ExtToolDir
try {
    $buildOutput = go build -buildvcs=false -o "$AnalysisTmp\ext.exe" . 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Build failed: $buildOutput"
        exit 1
    }
}
finally {
    Pop-Location
    $env:GOCACHE = $previousGoCache
}
Write-Host "Build successful."

# ---- Extraction Run 1 ----
Write-Host ""
Write-Host "--- Extraction Run 1 ---" -ForegroundColor Yellow
$extExe = "$AnalysisTmp\ext.exe"
$run1Output = & $extExe -input $SourceCopy -output $OutputDir1 -skip-format -strict=true 2>&1
$exit1 = $LASTEXITCODE
Write-Host $run1Output
if ($exit1 -ne 0) {
    Write-Error "Extraction Run 1 failed with exit code $exit1"
    exit 1
}
Write-Host "Run 1 exit code: $exit1 (PASS)"

# ---- Extraction Run 2 ----
Write-Host ""
Write-Host "--- Extraction Run 2 ---" -ForegroundColor Yellow
$run2Output = & $extExe -input $SourceCopy -output $OutputDir2 -skip-format -strict=true 2>&1
$exit2 = $LASTEXITCODE
Write-Host $run2Output
if ($exit2 -ne 0) {
    Write-Error "Extraction Run 2 failed with exit code $exit2"
    exit 1
}
Write-Host "Run 2 exit code: $exit2 (PASS)"

# ---- Compute file hashes ----
Write-Host ""
Write-Host "--- Byte Hash Comparison ---" -ForegroundColor Yellow

function Get-SortedFileHashes {
    param([string]$Dir)
    $files = Get-ChildItem -Path $Dir -Filter "*.proto" | Sort-Object Name
    $hashes = @{}
    foreach ($f in $files) {
        $hash = (Get-FileHash -Path $f.FullName -Algorithm SHA256).Hash
        $hashes[$f.Name] = $hash
        Write-Host "  $($f.Name): $hash"
    }
    return $hashes
}

function Get-SortedProtoNames {
    param([string]$Dir)
    return @(Get-ChildItem -Path $Dir -File -Filter "*.proto" | Sort-Object Name | ForEach-Object Name)
}

$run1Names = Get-SortedProtoNames $OutputDir1
$run2Names = Get-SortedProtoNames $OutputDir2
if ($run1Names.Count -eq 0) {
    Write-Error "Extraction produced no proto files"
    exit 1
}
$run1Set = $run1Names -join "`n"
$run2Set = $run2Names -join "`n"
if ($run1Set -cne $run2Set) {
    Write-Error "Run file sets differ:`n  Run 1: $($run1Names -join ', ')`n  Run 2: $($run2Names -join ', ')"
    exit 1
}
Write-Host "Run file sets: MATCH ($($run1Names -join ', '))" -ForegroundColor Green

Write-Host "Run 1 hashes:"
$hashes1 = Get-SortedFileHashes $OutputDir1
Write-Host ""
Write-Host "Run 2 hashes:"
$hashes2 = Get-SortedFileHashes $OutputDir2

Write-Host ""
$byteMatch = $true
foreach ($name in $run1Names) {
    if ($hashes1[$name] -cne $hashes2[$name]) {
        Write-Error "Byte hash mismatch for ${name}:`n  Run1: $($hashes1[$name])`n  Run2: $($hashes2[$name])"
        $byteMatch = $false
    }
}
if ($byteMatch) {
    Write-Host "Byte hash comparison: ALL MATCH (deterministic output confirmed)" -ForegroundColor Green
}
else {
    Write-Error "Byte hash comparison FAILED"
    exit 1
}

# ---- Parse all generated protos ----
Write-Host ""
Write-Host "--- Proto Parse Validation ---" -ForegroundColor Yellow
# Default-strict extraction calls validateGeneratedProtos, which parses every
# generated file with protoparse.Parser. Both extractor exits were checked above.
$allFiles = @(Get-ChildItem -Path $OutputDir1 -File -Filter "*.proto" | Sort-Object Name)
Write-Host "Parse validation: PASS ($($allFiles.Count) files parsed by strict extractor in both exit-zero runs)" -ForegroundColor Green

# ---- Compare normalized files to checked-in from_extensions ----
Write-Host ""
Write-Host "--- Checked-in Comparison ---" -ForegroundColor Yellow

$checkedInFiles = @("agent_v1.proto", "aiserver_v1.proto", "anyrun_v1.proto", "internapi_v1.proto")
$expectedNewFiles = @("git_forge_v1.proto", "origin_v1.proto")

function Get-NormalizedContent {
    param([string]$Path)
    $content = Get-Content $Path -Raw
    # Remove lines that differ by nature: go_package (path-dependent),
    # source comments with variable names (may differ across builds)
    $lines = $content -split "`r?`n"
    $filtered = @()
    foreach ($line in $lines) {
        if ($line -match '^\s*option go_package\s*=') { continue }
        if ($line -match '^\s*//\s*Source: .+ \(var: [^)]*\)\s*$' ) { continue }
        if ($line -match '^\s*//\s*Copied from: .+ \(var: [^)]*\)\s*$' ) { continue }
        $filtered += $line.TrimEnd()
    }
    return ($filtered -join "`n").Trim()
}

foreach ($file in $checkedInFiles) {
    $checkedInPath = Join-Path $FromExtensionsDir $file
    $generatedPath = Join-Path $OutputDir1 $file

    if (-not (Test-Path $checkedInPath)) {
        Write-Error "Checked-in file not found: $file"
        exit 1
    }
    if (-not (Test-Path $generatedPath)) {
        Write-Error "Generated file not found: $file"
        exit 1
    }

    $checkedInNorm = Get-NormalizedContent $checkedInPath
    $generatedNorm = Get-NormalizedContent $generatedPath

    if (Test-OrdinalEqual $checkedInNorm $generatedNorm) {
        Write-Host "  $file : MATCH" -ForegroundColor Green
    }
    else {
        Write-Host "  $file : MISMATCH (normalized content differs)" -ForegroundColor Red
        # Show first differing lines for debugging
        $ciLines = $checkedInNorm -split "`n"
        $genLines = $generatedNorm -split "`n"
        $maxLines = [Math]::Max($ciLines.Count, $genLines.Count)
        $diffCount = 0
        for ($i = 0; $i -lt $maxLines; $i++) {
            $ciLine = if ($i -lt $ciLines.Count) { $ciLines[$i] } else { "<missing>" }
            $genLine = if ($i -lt $genLines.Count) { $genLines[$i] } else { "<missing>" }
            if (-not (Test-OrdinalEqual $ciLine $genLine)) {
                $diffCount++
                if ($diffCount -le 10) {
                    Write-Host "    Line $($i+1):" -ForegroundColor Yellow
                    Write-Host "      Checked-in: $ciLine"
                    Write-Host "      Generated:  $genLine"
                }
            }
        }
        Write-Host "    Total differing lines: $diffCount"
        Write-Error "Normalized comparison failed for $file"
        exit 1
    }
}

# ---- Report newly generated files ----
Write-Host ""
Write-Host "--- New File Inventory ---" -ForegroundColor Yellow
$newFiles = @($run1Names | Where-Object { $checkedInFiles -notcontains $_ })
$newSet = $newFiles -join "`n"
$expectedNewSet = $expectedNewFiles -join "`n"
if ($newSet -cne $expectedNewSet) {
    Write-Error "Unexpected new-file inventory:`n  Expected: $($expectedNewFiles -join ', ')`n  Actual: $($newFiles -join ', ')"
    exit 1
}
foreach ($file in $newFiles) {
    Write-Host "  NEW: $file (not in checked-in from_extensions)" -ForegroundColor Magenta
}

# ---- Summary ----
Write-Host ""
Write-Host "=== VERIFICATION SUMMARY ===" -ForegroundColor Cyan
Write-Host "  SHA256:     $actualHash"
Write-Host "  Run 1 exit: $exit1"
Write-Host "  Run 2 exit: $exit2"
Write-Host "  File sets:  MATCH ($($run1Names.Count) files)"
Write-Host "  Byte hashes: MATCH (deterministic)"
Write-Host "  Parse:      PASS ($($allFiles.Count) files, strict protoparse in both runs)"
Write-Host "  Checked-in comparison: PASS ($($checkedInFiles.Count) files)"
if ($newFiles.Count -gt 0) {
    Write-Host "  New files:  $($newFiles -join ', ')"
}
else {
    Write-Host "  New files:  none"
}
Write-Host "=== VERIFICATION COMPLETE ===" -ForegroundColor Green

exit 0
