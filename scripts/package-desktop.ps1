$ErrorActionPreference = 'Stop'

$workspaceRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$desktopProject = Join-Path $workspaceRoot 'cmd\desktop'
$buildDirectory = Join-Path $desktopProject 'build'
$frontendDirectory = Join-Path $desktopProject 'frontend'
$nsisDirectory = Join-Path $buildDirectory 'windows\nsis'
$binaryPath = Join-Path $workspaceRoot 'bin\lune-desktop.exe'
$sysoPath = Join-Path $desktopProject 'lune_desktop_package_windows_amd64.syso'

$wails = Get-Command wails3 -ErrorAction SilentlyContinue
if ($null -eq $wails) {
	throw 'Install the pinned Wails CLI: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.30'
}

$go = Get-Command go -ErrorAction SilentlyContinue
if ($null -eq $go) { throw 'Go is required to build the desktop application.' }

$npm = Get-Command npm -ErrorAction SilentlyContinue
if ($null -eq $npm) { throw 'Node.js and npm are required to build the desktop frontend.' }

$makensis = Get-Command makensis -ErrorAction SilentlyContinue
if ($null -eq $makensis) {
	throw 'NSIS 3.x is required. Install NSIS and make makensis available on PATH.'
}

$windowsInfoPath = Join-Path $buildDirectory 'windows\info.json'
$windowsInfo = Get-Content -LiteralPath $windowsInfoPath -Raw | ConvertFrom-Json
$versionInfo = $windowsInfo.info.'0409'
if ($null -eq $versionInfo -or [string]::IsNullOrWhiteSpace($versionInfo.ProductVersion)) {
	throw "Windows version metadata is missing from $windowsInfoPath"
}
if ($versionInfo.ProductName -ne 'luneAgent' -or $versionInfo.CompanyName -ne 'afterlune') {
	throw 'Windows build assets do not match build/config.yml. Run wails3 task common:update:build-assets before packaging.'
}
$version = [string]$versionInfo.ProductVersion
if ($version -notmatch '^\d+\.\d+\.\d+$') {
	throw "Expected a three-part desktop version, got '$version'."
}
$goTarget = @(& $go.Source env GOOS GOARCH)
if ($LASTEXITCODE -ne 0 -or $goTarget.Count -lt 2 -or $goTarget[0] -ne 'windows' -or $goTarget[1] -ne 'amd64') {
	throw 'The installer script must run with a Windows amd64 Go toolchain.'
}

Push-Location $frontendDirectory
try {
	& $npm.Source ci
	if ($LASTEXITCODE -ne 0) { throw 'Could not install the locked frontend dependencies.' }
	& $npm.Source run build
	if ($LASTEXITCODE -ne 0) { throw 'Could not build the desktop frontend.' }
}
finally {
	Pop-Location
}

if (Test-Path -LiteralPath $sysoPath) {
	throw "Temporary Windows resource file already exists: $sysoPath"
}

$sysoCreated = $false
try {
	Push-Location $buildDirectory
	try {
		$sysoCreated = $true
		& $wails.Source generate syso -arch amd64 -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json -out ../lune_desktop_package_windows_amd64.syso
		if ($LASTEXITCODE -ne 0) { throw 'Could not generate Windows application resources.' }
	}
	finally {
		Pop-Location
	}

	New-Item -ItemType Directory -Force -Path (Split-Path -Parent $binaryPath) | Out-Null
	Push-Location $desktopProject
	try {
		& $go.Source build -tags production -trimpath -buildvcs=false '-ldflags=-w -s -H windowsgui' -o $binaryPath '.'
		if ($LASTEXITCODE -ne 0) { throw 'Could not build the luneAgent desktop executable.' }
	}
	finally {
		Pop-Location
	}
}
finally {
	if ($sysoCreated -and (Test-Path -LiteralPath $sysoPath)) {
		Remove-Item -LiteralPath $sysoPath -Force
	}
}
if (-not (Test-Path -LiteralPath $binaryPath)) { throw "Desktop build did not produce $binaryPath" }

$webViewBootstrapper = Join-Path $nsisDirectory 'MicrosoftEdgeWebview2Setup.exe'
if (-not (Test-Path -LiteralPath $webViewBootstrapper)) {
	& $wails.Source generate webview2bootstrapper -dir $nsisDirectory
	if ($LASTEXITCODE -ne 0) { throw 'Could not prepare the WebView2 bootstrapper.' }
}
if (-not (Test-Path -LiteralPath $webViewBootstrapper)) { throw 'The WebView2 bootstrapper is missing.' }

$artifactPaths = @()
foreach ($scope in @('user', 'machine')) {
	$artifactName = "luneAgent-$version-windows-amd64-$scope-setup.exe"
	$artifactPath = Join-Path $workspaceRoot "bin\$artifactName"
	$compilerArguments = @(
		'-V3'
		"-DWAILS_INSTALL_SCOPE=$scope"
		"-DREQUEST_EXECUTION_LEVEL=$(if ($scope -eq 'user') { 'user' } else { 'admin' })"
		"-DARG_WAILS_AMD64_BINARY=$binaryPath"
		"-DLUNE_INSTALLER_OUTFILE=$artifactPath"
		'project.nsi'
	)

	Push-Location $nsisDirectory
	try {
		& $makensis.Source @compilerArguments
		if ($LASTEXITCODE -ne 0) { throw "Could not build the $scope-scope installer." }
	}
	finally {
		Pop-Location
	}

	if (-not (Test-Path -LiteralPath $artifactPath)) { throw "Installer output is missing: $artifactPath" }
	$artifactPaths += $artifactPath
}

foreach ($artifactPath in $artifactPaths) {
	$hash = Get-FileHash -LiteralPath $artifactPath -Algorithm SHA256
	Write-Output "Built $($hash.Path)"
	Write-Output "SHA256 $($hash.Hash)"
}
