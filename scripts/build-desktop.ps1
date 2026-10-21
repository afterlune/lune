$ErrorActionPreference = 'Stop'

$workspaceRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$desktopProject = Join-Path $workspaceRoot 'cmd\desktop'
$outputPath = Join-Path $workspaceRoot 'bin\lune-desktop.exe'
$wails = Get-Command wails3 -ErrorAction SilentlyContinue
if ($null -eq $wails) {
	throw 'Install the pinned Wails CLI: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.34'
}

New-Item -ItemType Directory -Path (Split-Path -Parent $outputPath) -Force | Out-Null
Push-Location $desktopProject
try {
	& $wails.Source build GOOS=windows
	if ($LASTEXITCODE -ne 0) { throw 'Could not build luneAgent desktop.' }
	if (-not (Test-Path -LiteralPath $outputPath)) { throw "Desktop build did not produce $outputPath" }
	# DSH's Windows sandbox marks workspace files with a Low integrity label.
	# WebView2 cannot create its controller from a Low-labelled host image, so
	# restore the normal Medium level on the produced executable.
	& icacls $outputPath /setintegritylevel Medium *> $null
	if ($LASTEXITCODE -ne 0) {
		Write-Warning "Could not set the integrity level of $outputPath; WebView2 may fail to start from this workspace."
	}
	Write-Output "Built $outputPath"
}
finally {
	Pop-Location
}
