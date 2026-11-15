$ErrorActionPreference = 'Stop'

$workspaceRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$sourceDirectory = Join-Path $workspaceRoot 'bin'
$sourceApp = Join-Path $sourceDirectory 'lune.exe'
$sourceHelper = Join-Path $sourceDirectory 'lune-sandbox.exe'

foreach ($sourcePath in @($sourceApp, $sourceHelper)) {
	if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
		throw "Missing build output: $sourcePath. Run scripts/build.ps1 first."
	}
}

if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
	throw 'LOCALAPPDATA is not set; cannot choose a per-user install directory.'
}
if ([string]::IsNullOrWhiteSpace($env:APPDATA)) {
	throw 'APPDATA is not set; cannot choose a per-user config directory.'
}

$installDirectory = Join-Path $env:LOCALAPPDATA 'Programs\lune\bin'
$configDirectory = Join-Path $env:APPDATA 'lune'
$configPath = Join-Path $configDirectory 'config.json'

New-Item -ItemType Directory -Path $installDirectory -Force | Out-Null
New-Item -ItemType Directory -Path $configDirectory -Force | Out-Null
Copy-Item -LiteralPath $sourceApp -Destination (Join-Path $installDirectory 'lune.exe') -Force
Copy-Item -LiteralPath $sourceHelper -Destination (Join-Path $installDirectory 'lune-sandbox.exe') -Force

# Install engram (Persistent memory for AI coding agents via MCP)
$engramInstalled = $false
try {
	Write-Output "Installing / updating engram via 'go install github.com/Gentleman-Programming/engram/v3/cmd/engram@latest'..."
	& go install -trimpath github.com/Gentleman-Programming/engram/v3/cmd/engram@latest
	if ($LASTEXITCODE -eq 0) {
		$goBin = (go env GOBIN).Trim()
		$goPath = (go env GOPATH).Trim()
		$candidates = @()
		if (-not [string]::IsNullOrWhiteSpace($goBin)) {
			$candidates += (Join-Path $goBin 'engram.exe')
		}
		if (-not [string]::IsNullOrWhiteSpace($goPath)) {
			$candidates += (Join-Path $goPath 'bin\engram.exe')
		}
		$candidates += (Join-Path $env:USERPROFILE 'go\bin\engram.exe')

		foreach ($candidate in $candidates) {
			if (Test-Path -LiteralPath $candidate -PathType Leaf) {
				Copy-Item -LiteralPath $candidate -Destination (Join-Path $installDirectory 'engram.exe') -Force
				$engramInstalled = $true
				Write-Output "Installed engram.exe to $installDirectory"
				break
			}
		}
	}
} catch {
	Write-Warning "Could not build/install engram: $_"
}

$createdConfig = $false
if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) {
	$workspaceConfig = Join-Path $workspaceRoot 'config.json'
	if (Test-Path -LiteralPath $workspaceConfig -PathType Leaf) {
		Copy-Item -LiteralPath $workspaceConfig -Destination $configPath
	} else {
		$exampleConfig = Join-Path $workspaceRoot 'config.example.json'
		if (-not (Test-Path -LiteralPath $exampleConfig -PathType Leaf)) {
			throw "Neither config.json nor config.example.json was found in $workspaceRoot."
		}
		Copy-Item -LiteralPath $exampleConfig -Destination $configPath
		$createdConfig = $true
	}
}

# Ensure existing config has default engram MCP server configuration
if (Test-Path -LiteralPath $configPath -PathType Leaf) {
	try {
		$rawText = [System.IO.File]::ReadAllText($configPath)
		$configObj = $rawText | ConvertFrom-Json
		if ($null -eq $configObj.mcpServers) {
			$configObj | Add-Member -MemberType NoteProperty -Name 'mcpServers' -Value ([PSCustomObject]@{})
		}
		if ($null -eq $configObj.mcpServers.engram) {
			$engramConfig = [PSCustomObject]@{
				transport = 'stdio'
				command   = 'engram'
				args      = @('mcp', '--tools=agent')
			}
			$configObj.mcpServers | Add-Member -MemberType NoteProperty -Name 'engram' -Value $engramConfig
			$updatedJson = $configObj | ConvertTo-Json -Depth 10
			[System.IO.File]::WriteAllText($configPath, $updatedJson + "`r`n")
			Write-Output "Configured default 'engram' MCP server in $configPath"
		}
	} catch {
		Write-Warning "Could not update $configPath with engram config: $_"
	}
}

$normalizedInstallDirectory = [System.IO.Path]::GetFullPath($installDirectory).TrimEnd([char]'\')
$userPathValue = [Environment]::GetEnvironmentVariable('Path', 'User')
$userPathEntries = @()
if (-not [string]::IsNullOrWhiteSpace($userPathValue)) {
	$userPathEntries = @($userPathValue -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
}
$userPathContainsInstallDirectory = $false
foreach ($entry in $userPathEntries) {
	$expandedEntry = [Environment]::ExpandEnvironmentVariables($entry.Trim()).TrimEnd([char]'\')
	if ([string]::Equals($expandedEntry, $normalizedInstallDirectory, [StringComparison]::OrdinalIgnoreCase)) {
		$userPathContainsInstallDirectory = $true
		break
	}
}
if (-not $userPathContainsInstallDirectory) {
	$userPathEntries += $normalizedInstallDirectory
	[Environment]::SetEnvironmentVariable('Path', ($userPathEntries -join ';'), 'User')
}

$processPathEntries = @($env:Path -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
$processPathContainsInstallDirectory = $false
foreach ($entry in $processPathEntries) {
	$expandedEntry = [Environment]::ExpandEnvironmentVariables($entry.Trim()).TrimEnd([char]'\')
	if ([string]::Equals($expandedEntry, $normalizedInstallDirectory, [StringComparison]::OrdinalIgnoreCase)) {
		$processPathContainsInstallDirectory = $true
		break
	}
}
if (-not $processPathContainsInstallDirectory) {
	$env:Path = (($processPathEntries + $normalizedInstallDirectory) -join ';')
}

if ($engramInstalled) {
	Write-Output "Installed lune.exe, lune-sandbox.exe, and engram.exe to $installDirectory"
} else {
	Write-Output "Installed lune.exe and lune-sandbox.exe to $installDirectory"
}
Write-Output "User config: $configPath"
if ($createdConfig) {
	Write-Output 'Created config.json from config.example.json. Edit it and add your provider API key before starting Lune.'
}
