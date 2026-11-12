#!/usr/bin/env bash
# Developer ID signing and notarization for the macOS release (CI only).
#
#   sign-notarize.sh app <path/to/dbedit.app>   import the certificate into a
#       temporary keychain and sign the bundle (hardened runtime, timestamp,
#       build/darwin/entitlements.plist)
#   sign-notarize.sh dmg <path/to/dbedit.dmg>   sign the dmg and, when the Apple
#       ID secrets are present, notarize it with notarytool and staple it
#   sign-notarize.sh cleanup                    delete the temporary keychain
#
# Inputs come from the environment (GitHub secrets); nothing is stored in the
# repository:
#   MACOS_CERT_P12        base64 of a "Developer ID Application" .p12
#   MACOS_CERT_PASSWORD   its password
#   APPLE_ID, APPLE_TEAM_ID, APPLE_APP_PASSWORD   notarization (app-specific
#                         password of the Apple ID)
# The release workflow only calls this when MACOS_CERT_P12/PASSWORD are set;
# without them the .app keeps its ad-hoc signature and the dmg is unsigned.
set -euo pipefail

cd "$(dirname "$0")/../.."
KEYCHAIN="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/dbedit-signing.keychain-db"
ENTITLEMENTS="build/darwin/entitlements.plist"

need() {
	for v in "$@"; do
		if [ -z "${!v:-}" ]; then
			echo "sign-notarize: $v is not set" >&2
			exit 1
		fi
	done
}

identity() {
	security find-identity -v -p codesigning "$KEYCHAIN" |
		awk -F'"' '/Developer ID Application/ { print $2; exit }'
}

case "${1:-}" in
app)
	app="${2:?usage: sign-notarize.sh app <dbedit.app>}"
	need MACOS_CERT_P12 MACOS_CERT_PASSWORD
	kcpass="$(openssl rand -base64 24)"
	security create-keychain -p "$kcpass" "$KEYCHAIN"
	security set-keychain-settings -lut 21600 "$KEYCHAIN"
	security unlock-keychain -p "$kcpass" "$KEYCHAIN"
	p12="$(mktemp)"
	trap 'rm -f "$p12"' EXIT
	printf '%s' "$MACOS_CERT_P12" | base64 --decode >"$p12"
	security import "$p12" -k "$KEYCHAIN" -P "$MACOS_CERT_PASSWORD" -T /usr/bin/codesign
	security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$kcpass" "$KEYCHAIN" >/dev/null
	# Search the temporary keychain first, keep the existing ones.
	# shellcheck disable=SC2046
	security list-keychains -d user -s "$KEYCHAIN" $(security list-keychains -d user | tr -d '"')
	id="$(identity)"
	if [ -z "$id" ]; then
		echo "sign-notarize: no 'Developer ID Application' identity in MACOS_CERT_P12" >&2
		exit 1
	fi
	echo "Signing $app as: $id"
	xattr -cr "$app"
	# Single-binary bundle: sign the executable, then the bundle (no --deep).
	codesign --force --options runtime --timestamp --entitlements "$ENTITLEMENTS" --sign "$id" "$app/Contents/MacOS/dbedit"
	codesign --force --options runtime --timestamp --entitlements "$ENTITLEMENTS" --sign "$id" "$app"
	codesign --verify --strict --verbose=2 "$app"
	;;
dmg)
	dmg="${2:?usage: sign-notarize.sh dmg <dbedit.dmg>}"
	id="$(identity)"
	if [ -z "$id" ]; then
		echo "sign-notarize: run 'sign-notarize.sh app' first" >&2
		exit 1
	fi
	codesign --force --timestamp --sign "$id" "$dmg"
	if [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ]; then
		xcrun notarytool submit "$dmg" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" \
			--password "$APPLE_APP_PASSWORD" --wait --timeout 30m
		xcrun stapler staple "$dmg"
		xcrun stapler validate "$dmg"
		spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg"
	else
		echo "sign-notarize: APPLE_ID/APPLE_TEAM_ID/APPLE_APP_PASSWORD not set; dmg signed but not notarized"
	fi
	;;
cleanup)
	security delete-keychain "$KEYCHAIN" 2>/dev/null || true
	;;
*)
	echo "usage: sign-notarize.sh app <dbedit.app> | dmg <dbedit.dmg> | cleanup" >&2
	exit 2
	;;
esac
