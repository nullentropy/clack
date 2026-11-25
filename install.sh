#!/bin/sh
set -eu

repo=${CLACK_REPO:-nullentropy/clack}
url=${CLACK_URL:-}

fail() {
	echo "install.sh: $*" >&2
	exit 1
}

case $(uname -s) in
Darwin)
	os=macos
	asset=clack-macos-universal.zip
	dest=${CLACK_DEST:-/Applications}
	;;
Linux)
	os=linux
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "no Linux build for $(uname -m); build from source instead" ;;
	esac
	asset=clack-linux-$arch.tar.gz
	dest=${CLACK_DEST:-$HOME/.local}
	;;
*)
	fail "no build for $(uname -s); on Windows run install.ps1"
	;;
esac

if [ -z "$url" ]; then
	case $repo in
	*/*) ;;
	*) fail "CLACK_REPO must be in owner/name form, such as nullentropy/clack, got: $repo" ;;
	esac
	url="https://github.com/$repo/releases/latest/download/$asset"
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

download() {
	echo "Downloading $url"
	curl -fSL --progress-bar -o "$work/$asset" "$url" || fail "download failed: $url"
}

install_macos() {
	[ -d "$dest" ] || fail "$dest does not exist"
	[ -w "$dest" ] || fail "cannot write to $dest; run as an admin user or set CLACK_DEST"
	app="$dest/clack.app"
	download
	ditto -x -k "$work/$asset" "$work/unzipped" || fail "could not unzip $asset"
	[ -d "$work/unzipped/clack.app" ] || fail "$asset has no clack.app"

	exe="$app/Contents/MacOS/"
	if pgrep -f "$exe" >/dev/null 2>&1; then
		echo "Quitting the running clack"
		osascript -e "tell application \"$app\" to quit" >/dev/null 2>&1 || true
		i=0
		while pgrep -f "$exe" >/dev/null 2>&1 && [ $i -lt 20 ]; do
			sleep 0.5
			i=$((i + 1))
		done
		if pgrep -f "$exe" >/dev/null 2>&1; then
			pkill -TERM -f "$exe" || true
			sleep 2
			pkill -KILL -f "$exe" 2>/dev/null || true
		fi
	fi

	rm -rf "$app"
	mv "$work/unzipped/clack.app" "$app"
	echo "Installed clack to $app"
}

install_linux() {
	mkdir -p "$dest" || fail "cannot create $dest"
	[ -w "$dest" ] || fail "cannot write to $dest; set CLACK_DEST"
	download
	mkdir "$work/unpacked"
	tar -xzf "$work/$asset" -C "$work/unpacked" || fail "could not unpack $asset"
	[ -f "$work/unpacked/bin/clack" ] || fail "$asset has no bin/clack"

	exe="$dest/bin/clack"
	if pgrep -f "^$exe( |\$)" >/dev/null 2>&1; then
		echo "Quitting the running clack"
		pkill -TERM -f "^$exe( |\$)" || true
		i=0
		while pgrep -f "^$exe( |\$)" >/dev/null 2>&1 && [ $i -lt 20 ]; do
			sleep 0.5
			i=$((i + 1))
		done
		pkill -KILL -f "^$exe( |\$)" 2>/dev/null || true
	fi

	mkdir -p "$dest/bin" "$dest/share"
	cp "$work/unpacked/bin/clack" "$dest/bin/.clack.new"
	chmod 755 "$dest/bin/.clack.new"
	mv -f "$dest/bin/.clack.new" "$exe"
	cp -R "$work/unpacked/share/." "$dest/share/"
	entry="$dest/share/applications/clack.desktop"
	sed "s|^Exec=.*|Exec=$exe|" "$entry" >"$work/entry" && cat "$work/entry" >"$entry"
	if command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "$dest/share/applications" >/dev/null 2>&1 || true
	fi
	echo "Installed clack to $exe"
}

case $os in
macos) install_macos ;;
linux) install_linux ;;
esac
