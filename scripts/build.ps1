$ErrorActionPreference = 'Stop'

$workspaceRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$outputDirectory = Join-Path $workspaceRoot 'bin'
$helperName = 'lune-sandbox'
$appName = 'lune'
if ($env:OS -eq 'Windows_NT') {
	$helperName += '.exe'
	$appName += '.exe'
}
$helperPath = Join-Path $outputDirectory $helperName
$appPath = Join-Path $outputDirectory $appName

New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
Push-Location $workspaceRoot
try {
	& go build -trimpath -o $helperPath github.com/afterlune/luneCore/cmd/lune-sandbox
	if ($LASTEXITCODE -ne 0) { throw 'Could not build lune-sandbox.' }

	& go build -trimpath -o $appPath ./cmd
	if ($LASTEXITCODE -ne 0) { throw 'Could not build lune.' }

	Write-Output "Built $appPath and $helperPath"
}
finally {
	Pop-Location
}
