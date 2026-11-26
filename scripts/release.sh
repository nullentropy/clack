#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
root=$(pwd)

if [ $# -gt 1 ]; then
	echo "usage: scripts/release.sh [version]" >&2
	exit 2
fi

if [ -n "$(git status --porcelain)" ]; then
	echo "working tree is not clean; commit or stash first" >&2
	exit 1
fi

if [ $# -eq 1 ]; then
	version=$1
else
	version=$(git describe --tags --exact-match 2>/dev/null) || {
		echo "HEAD has no tag; tag it or pass a version: scripts/release.sh 1.2.3" >&2
		exit 1
	}
fi
version=${version#v}
case $version in
'' | *[!0-9A-Za-z.-]*)
	echo "bad version: $version" >&2
	exit 1
	;;
esac

out="$root/release"
work=$(mktemp -d)
cleanup() {
	git -C "$root" worktree remove --force "$work/src" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

git worktree add --detach --quiet "$work/src" HEAD
printf 'package main\n\nfunc init() { packaged = true }\n' >"$work/src/packaged_release.go"
mkdir -p "$out"

release_macos() {
	bundle_version=$(printf '%s' "$version" | sed 's/[^0-9.].*//; s/\.$//')
	[ -n "$bundle_version" ] || bundle_version=0
	min_macos=13.0
	zip="$out/clack-$version-macos-universal.zip"

	for arch in arm64 amd64; do
		(cd "$work/src" && CGO_ENABLED=1 GOOS=darwin GOARCH=$arch \
			MACOSX_DEPLOYMENT_TARGET=$min_macos \
			CGO_CFLAGS="-O2 -g -mmacosx-version-min=$min_macos" \
			CGO_LDFLAGS="-mmacosx-version-min=$min_macos" \
			go build -trimpath -ldflags "-X main.version=$version" -o "$work/clack-$arch" .)
		minos=$(vtool -show-build "$work/clack-$arch" | awk '$1 == "minos" { print $2 }')
		if [ "$minos" != "$min_macos" ]; then
			echo "$arch binary declares macOS $minos, want $min_macos" >&2
			exit 1
		fi
	done
	lipo -create -output "$work/clack" "$work/clack-arm64" "$work/clack-amd64"

	(cd "$work/src" && go run github.com/nullentropy/caution/cmd/appbundle -bin "$work/clack" -name clack -id dev.caution.clack -icon icon.png -o "$work/stage")
	app="$work/stage/clack.app"
	plutil -replace CFBundleShortVersionString -string "$version" "$app/Contents/Info.plist"
	plutil -replace CFBundleVersion -string "$bundle_version" "$app/Contents/Info.plist"
	plutil -replace LSMinimumSystemVersion -string "$min_macos" "$app/Contents/Info.plist"
	codesign --force -s - "$app"
	codesign --verify --strict "$app"

	latest="$out/clack-macos-universal.zip"
	rm -f "$zip" "$latest"
	ditto -c -k --sequesterRsrc --keepParent "$app" "$zip"
	cp "$zip" "$latest"
	echo "$zip"
	echo "$latest"
}

release_linux() {
	arch=$(go env GOARCH)
	tgz="$out/clack-$version-linux-$arch.tar.gz"
	(cd "$work/src" && CGO_ENABLED=1 go build -trimpath -ldflags "-X main.version=$version" -o "$work/clack" .)
	(cd "$work/src" && go run github.com/nullentropy/caution/cmd/linuxapp -bin "$work/clack" -name clack -id clack -icon icon.png \
		-comment "Slack desktop client" -categories "Network;Chat;" -o "$work/stage")

	latest="$out/clack-linux-$arch.tar.gz"
	rm -f "$tgz" "$latest"
	tar -C "$work/stage" -czf "$tgz" bin share
	cp "$tgz" "$latest"
	echo "$tgz"
	echo "$latest"
}

case $(uname -s) in
Darwin) release_macos ;;
Linux) release_linux ;;
*)
	echo "scripts/release.sh runs on macOS and Linux; on Windows run scripts/release.ps1" >&2
	exit 1
	;;
esac
