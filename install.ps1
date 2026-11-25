& {
	$ErrorActionPreference = 'Stop'
	$repo = if ($env:CLACK_REPO) { $env:CLACK_REPO } else { 'nullentropy/clack' }
	$dest = if ($env:CLACK_DEST) { $env:CLACK_DEST } else { Join-Path $env:LOCALAPPDATA 'Programs\clack' }
	$asset = 'clack-windows-amd64.zip'
	$url = $env:CLACK_URL
	if (-not $url) {
		if ($repo -notmatch '^[^/]+/[^/]+$') {
			throw "install.ps1: CLACK_REPO must be in owner/name form, such as nullentropy/clack, got: $repo"
		}
		$url = "https://github.com/$repo/releases/latest/download/$asset"
	}

	$work = Join-Path ([IO.Path]::GetTempPath()) ('clack-' + [guid]::NewGuid())
	New-Item -ItemType Directory -Path $work | Out-Null
	try {
		Write-Host "Downloading $url"
		$zip = Join-Path $work $asset
		$ProgressPreference = 'SilentlyContinue'
		Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing
		Expand-Archive -Path $zip -DestinationPath (Join-Path $work 'unzipped')
		$exe = Join-Path $work 'unzipped\clack.exe'
		if (-not (Test-Path $exe)) {
			throw "install.ps1: $asset has no clack.exe"
		}

		$target = Join-Path $dest 'clack.exe'
		$running = @(Get-Process -Name clack -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $target })
		if ($running.Count -gt 0) {
			Write-Host 'Quitting the running clack'
			$running | ForEach-Object { [void]$_.CloseMainWindow() }
			$running | Wait-Process -Timeout 10 -ErrorAction SilentlyContinue
			$running | Where-Object { -not $_.HasExited } | Stop-Process -Force
		}

		New-Item -ItemType Directory -Force -Path $dest | Out-Null
		Copy-Item -Force $exe $target

		$link = Join-Path ([Environment]::GetFolderPath('Programs')) 'clack.lnk'
		$shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut($link)
		$shortcut.TargetPath = $target
		$shortcut.WorkingDirectory = $dest
		$shortcut.Save()
		Write-Host "Installed clack to $target"
	} finally {
		Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $work
	}
}
