#!/usr/bin/env bash
# Developer ID signing and notarization for the macOS release (CI only).
#
#   sign-notarize.sh app <path/to/lontar.app>   import the certificate into a
#       temporary keychain and sign the bundle (hardened runtime, timestamp,
#       build/darwin/entitlements.plist)
#   sign-notarize.sh dmg <path/to/lontar.dmg>   sign the dmg and, when the Apple
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
KEYCHAIN="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/lontar-signing.keychain-db"
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
	app="${2:?usage: sign-notarize.sh app <lontar.app>}"
	need MACOS_CERT_P12 MACOS_CERT_PASSWORD
	kcpass="$(openssl rand -base64 24)"
	security create-keychain -p "$kcpass" "$KEYCHAIN"
	security set-keychain-settings -lut 21600 "$KEYCHAIN"
	security unlock-keychain -p "$kcpass" "$KEYCHAIN"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	p12="$tmp/cert.p12"
	# Tolerate base64 that was wrapped or pasted with a trailing newline.
	printf '%s' "$MACOS_CERT_P12" | tr -d ' \r\n' | base64 --decode >"$p12"
	# A PKCS#12 file is DER: it starts with a SEQUENCE byte (0x30). Say so
	# instead of letting `security` report an opaque "Unknown format".
	if [ "$(head -c1 "$p12" | od -An -tx1 | tr -d ' \n')" != "30" ]; then
		echo "sign-notarize: MACOS_CERT_P12 does not decode to a .p12 file (expected: base64 -w0 DeveloperID.p12)" >&2
		exit 1
	fi
	# -f/-t explicitly: without them `security` guesses the format from the
	# file name.
	security import "$p12" -f pkcs12 -t agg -k "$KEYCHAIN" -P "$MACOS_CERT_PASSWORD" -T /usr/bin/codesign
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
	codesign --force --options runtime --timestamp --entitlements "$ENTITLEMENTS" --sign "$id" "$app/Contents/MacOS/lontar"
	codesign --force --options runtime --timestamp --entitlements "$ENTITLEMENTS" --sign "$id" "$app"
	codesign --verify --strict --verbose=2 "$app"
	;;
dmg)
	dmg="${2:?usage: sign-notarize.sh dmg <lontar.dmg>}"
	id="$(identity)"
	if [ -z "$id" ]; then
		echo "sign-notarize: run 'sign-notarize.sh app' first" >&2
		exit 1
	fi
	codesign --force --timestamp --sign "$id" "$dmg"
	if [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ]; then
		auth=(--apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD")
		out="$(mktemp)"
		# Apple's notary service sometimes answers with HTTP 5xx; retry those.
		# A submission that Apple rejected (status Invalid) is not retried:
		# its log says what to fix.
		for attempt in 1 2 3 4; do
			xcrun notarytool submit "$dmg" "${auth[@]}" --wait --timeout 30m 2>&1 | tee "$out" || true
			grep -q 'status: Accepted' "$out" && break
			sub="$(awk '$1 == "id:" { print $2; exit }' "$out")"
			if grep -q 'status: \(Invalid\|Rejected\)' "$out"; then
				[ -n "$sub" ] && xcrun notarytool log "$sub" "${auth[@]}" || true
				echo "sign-notarize: Apple rejected the submission (see the log above)" >&2
				exit 1
			fi
			if [ "$attempt" = 4 ]; then
				echo "sign-notarize: notarization failed after $attempt attempts" >&2
				exit 1
			fi
			echo "sign-notarize: notarization attempt $attempt failed; retrying in $((attempt * 60))s" >&2
			sleep $((attempt * 60))
		done
		rm -f "$out"
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
	echo "usage: sign-notarize.sh app <lontar.app> | dmg <lontar.dmg> | cleanup" >&2
	exit 2
	;;
esac
