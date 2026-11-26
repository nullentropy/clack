param([string]$Version)
$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$root = (Get-Location).Path

if (git status --porcelain) {
	throw 'working tree is not clean; commit or stash first'
}
if (-not $Version) {
	$Version = git describe --tags --exact-match 2>$null
	if (-not $Version) {
		throw 'HEAD has no tag; tag it or pass a version: scripts/release.ps1 1.2.3'
	}
}
$Version = $Version -replace '^v', ''
if ($Version -notmatch '^[0-9A-Za-z.-]+$') {
	throw "bad version: $Version"
}

$out = Join-Path $root 'release'
$work = Join-Path ([IO.Path]::GetTempPath()) ('clack-' + [guid]::NewGuid())
$src = Join-Path $work 'src'
$stage = Join-Path $work 'stage'
New-Item -ItemType Directory -Force -Path $out, $stage | Out-Null
git worktree add --detach --quiet $src HEAD
if ($LASTEXITCODE -ne 0) { throw 'git worktree add failed' }
try {
	Set-Content -Path (Join-Path $src 'packaged_release.go') -Value "package main`n`nfunc init() { packaged = true }"
	Set-Content -Path (Join-Path $src 'version_release.go') -Value "package main`n`nfunc init() { version = `"$Version`" }"
	Push-Location $src
	go run github.com/nullentropy/caution/cmd/winapp -pkg . -name clack -id dev.caution.clack -icon icon.png -o $stage -cc gcc
	if ($LASTEXITCODE -ne 0) { throw 'winapp failed' }
	Pop-Location

	$zip = Join-Path $out "clack-$Version-windows-amd64.zip"
	$latest = Join-Path $out 'clack-windows-amd64.zip'
	Remove-Item -Force -ErrorAction SilentlyContinue $zip, $latest
	Compress-Archive -Path (Join-Path $stage 'clack.exe') -DestinationPath $zip
	Copy-Item $zip $latest
	Write-Output $zip
	Write-Output $latest
} finally {
	Set-Location $root
	git worktree remove --force $src 2>$null
	Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $work
}
