$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User") + ";$HOME\go\bin"

Set-Location "E:\MyProject\cursor-byok\build"
& wails3 generate syso -arch amd64 -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json -out ../wails_windows_amd64.syso 2>&1
if ($LASTEXITCODE -ne 0) { Write-Host "SYSO_FAILED"; exit 1 }

Set-Location "E:\MyProject\cursor-byok"
& go build -tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui -X cursor/internal/buildinfo.Version=0.0.56" -o bin/windows-64-test.exe 2>&1
if ($LASTEXITCODE -ne 0) { Write-Host "BUILD_FAILED"; exit 1 }

Remove-Item -Force wails_windows_amd64.syso -ErrorAction SilentlyContinue

Set-Location "E:\MyProject\cursor-byok\bin"
Compress-Archive -Path windows-64-test.exe -DestinationPath windows-64-test.zip -Force
Remove-Item windows-64-test.exe
Write-Host "DONE"
