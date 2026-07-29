$env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User") + ";$HOME\go\bin"
$root = "E:\MyProject\cursor-byok"

Set-Location "$root\build"
& wails3 generate syso -arch amd64 -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json -out ../wails_windows_amd64.syso
if ($LASTEXITCODE -ne 0) { throw "wails syso generation failed" }

Set-Location $root
& go build -tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui -X cursor/internal/buildinfo.Version=0.0.57" -o bin/windows-64-overlay-fixed.exe
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

Remove-Item -Force "$root\wails_windows_amd64.syso" -ErrorAction SilentlyContinue
Set-Location "$root\bin"
Compress-Archive -Path windows-64-overlay-fixed.exe -DestinationPath windows-64-overlay-fixed.zip -Force
Remove-Item windows-64-overlay-fixed.exe
Get-Item windows-64-overlay-fixed.zip | Select-Object FullName, Length, LastWriteTime
