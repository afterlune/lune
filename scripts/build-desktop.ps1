$ErrorActionPreference = 'Stop'

$workspaceRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$desktopProject = Join-Path $workspaceRoot 'cmd\desktop'
$outputPath = Join-Path $workspaceRoot 'bin\lune-desktop.exe'
$wails = Get-Command wails3 -ErrorAction SilentlyContinue
if ($null -eq $wails) {
	throw 'Install the pinned Wails CLI: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.30'
}

New-Item -ItemType Directory -Path (Split-Path -Parent $outputPath) -Force | Out-Null
Push-Location $desktopProject
try {
	& $wails.Source build GOOS=windows
	if ($LASTEXITCODE -ne 0) { throw 'Could not build luneAgent desktop.' }
	if (-not (Test-Path -LiteralPath $outputPath)) { throw "Desktop build did not produce $outputPath" }
	Write-Output "Built $outputPath"
}
finally {
	Pop-Location
}
