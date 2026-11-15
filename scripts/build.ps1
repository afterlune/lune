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
$linuxOutputDirectory = $outputDirectory

$originalGoos = $env:GOOS
$originalGoarch = $env:GOARCH
$originalCgoEnabled = $env:CGO_ENABLED
$originalGowork = $env:GOWORK

New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
Push-Location $workspaceRoot
try {
	if ([string]::IsNullOrWhiteSpace($env:GOWORK)) { $env:GOWORK = 'off' }
	$ldflags = '-s -w'
	& go build -trimpath -ldflags $ldflags -o $helperPath github.com/afterlune/luneCore/cmd/lune-sandbox
	if ($LASTEXITCODE -ne 0) { throw 'Could not build lune-sandbox.' }

	& go build -trimpath -ldflags $ldflags -o $appPath ./cmd
	if ($LASTEXITCODE -ne 0) { throw 'Could not build lune.' }

	$env:CGO_ENABLED = '0'
	foreach ($linuxArchitecture in @('amd64', 'arm64')) {
		$env:GOOS = 'linux'
		$env:GOARCH = $linuxArchitecture
		$targetDirectory = Join-Path $linuxOutputDirectory "linux-$linuxArchitecture"
		New-Item -ItemType Directory -Path $targetDirectory -Force | Out-Null

		& go build -trimpath -ldflags $ldflags -o (Join-Path $targetDirectory 'lune-sandbox') github.com/afterlune/luneCore/cmd/lune-sandbox
		if ($LASTEXITCODE -ne 0) { throw "Could not build Linux lune-sandbox ($linuxArchitecture)." }

		& go build -trimpath -ldflags $ldflags -o (Join-Path $targetDirectory 'lune') ./cmd
		if ($LASTEXITCODE -ne 0) { throw "Could not build Linux lune ($linuxArchitecture)." }
	}

	Write-Output "Built $appPath, $helperPath, and Linux amd64/arm64 binaries under $linuxOutputDirectory."
}
finally {
	if ($null -eq $originalGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $originalGoos }
	if ($null -eq $originalGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $originalGoarch }
	if ($null -eq $originalCgoEnabled) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $originalCgoEnabled }
	if ($null -eq $originalGowork) { Remove-Item Env:GOWORK -ErrorAction SilentlyContinue } else { $env:GOWORK = $originalGowork }
	Pop-Location
}
