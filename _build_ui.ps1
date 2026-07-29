$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User") + ";$HOME\go\bin"
Set-Location "E:\MyProject\cursor-byok\frontend"
& node ./scripts/run-vite-build.mjs --mode production 2>&1
if ($LASTEXITCODE -ne 0) { Write-Host "FRONTEND_FAILED"; exit 1 }
Write-Host "FRONTEND_OK"
