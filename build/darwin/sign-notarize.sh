#!/usr/bin/env bash
# Developer ID signing and notarization for the macOS release (CI only).
#
#   sign-notarize.sh app <path/to/lontar.app>   import the certificate into a
#       temporary keychain and sign the bundle (hardened runtime, timestamp,
#       build/darwin/entitlements.plist)
#   sign-notarize.sh dmg <path/to/lontar.dmg>   sign the dmg and, when
#       notarization secrets are present, notarize it with notarytool and
#       staple it
#   sign-notarize.sh cleanup                    delete the temporary keychain
#
# Inputs come from the environment (GitHub secrets); nothing is stored in the
# repository:
#   MACOS_CERT_P12        base64 of a "Developer ID Application" .p12
#   MACOS_CERT_PASSWORD   its password
# Notarization uses, in this order, whichever is complete:
#   APPLE_API_KEY, APPLE_API_KEY_ID[, APPLE_API_ISSUER]   App Store Connect API
#                         key: the .p8 contents (or their base64), its Key ID
#                         and, for a team key, the Issuer ID
#   APPLE_ID, APPLE_TEAM_ID, APPLE_APP_PASSWORD   Apple ID with an
#                         app-specific password
# Their format is checked first and the credentials are tried against Apple
# before the upload, so a wrong secret is named instead of failing later.
# Errors never print secret values.
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

# trim VALUE: VALUE without any whitespace (none of the notarization secrets
# contains any; a pasted trailing newline is a common mistake).
trim() { printf '%s' "$1" | tr -d '[:space:]'; }

bad() {
	echo "sign-notarize: secret $1 looks wrong: expected $2" >&2
	exit 1
}

# notary_auth: sets AUTH to notarytool's credential flags, or to nothing when
# no notarization secrets are set. Writes an API key to $TMPD.
notary_auth() {
	AUTH=()
	local key_id issuer key apple_id team pass v
	for v in APPLE_API_KEY_ID APPLE_API_ISSUER APPLE_ID APPLE_TEAM_ID APPLE_APP_PASSWORD; do
		if [ -n "${!v:-}" ] && [ "$(trim "${!v}")" != "${!v}" ]; then
			echo "sign-notarize: note: secret $v contains whitespace (e.g. a trailing newline); ignoring it"
		fi
	done
	key_id="$(trim "${APPLE_API_KEY_ID:-}")"
	if [ -n "${APPLE_API_KEY:-}" ] && [ -n "$key_id" ]; then
		issuer="$(trim "${APPLE_API_ISSUER:-}")"
		[[ "$key_id" =~ ^[A-Z0-9]{10}$ ]] || bad APPLE_API_KEY_ID "the 10-character Key ID, e.g. 2X9R4HXF34"
		[ -z "$issuer" ] || [[ "$issuer" =~ ^[0-9a-fA-F-]{36}$ ]] ||
			bad APPLE_API_ISSUER "the Issuer ID, a UUID like 69a6de7f-01c2-47e3-e053-5b8c7c11a4d1"
		key="$APPLE_API_KEY"
		if [[ "$key" != *"BEGIN PRIVATE KEY"* ]]; then
			key="$(printf '%s' "$key" | tr -d ' \r\n' | base64 --decode 2>/dev/null || true)"
		fi
		[[ "$key" == *"BEGIN PRIVATE KEY"* ]] || bad APPLE_API_KEY "the contents of the AuthKey_<KeyID>.p8 file"
		printf '%s\n' "$key" >"$TMPD/AuthKey_$key_id.p8"
		AUTH=(--key "$TMPD/AuthKey_$key_id.p8" --key-id "$key_id")
		[ -z "$issuer" ] || AUTH+=(--issuer "$issuer")
		echo "sign-notarize: notarizing with an App Store Connect API key"
	elif [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ]; then
		apple_id="$(trim "$APPLE_ID")"
		team="$(trim "$APPLE_TEAM_ID")"
		pass="$(trim "$APPLE_APP_PASSWORD")"
		[[ "$apple_id" == ?*@?*.?* ]] || bad APPLE_ID "the e-mail address of the Apple ID"
		[[ "$team" =~ ^[A-Z0-9]{10}$ ]] || bad APPLE_TEAM_ID "the 10-character Team ID in capitals, e.g. ABCDE12345"
		[[ "$pass" =~ ^[a-z]{4}-[a-z]{4}-[a-z]{4}-[a-z]{4}$ ]] ||
			bad APPLE_APP_PASSWORD "an app-specific password like abcd-efgh-ijkl-mnop, not the Apple ID's own password"
		AUTH=(--apple-id "$apple_id" --team-id "$team" --password "$pass")
		echo "sign-notarize: notarizing with an Apple ID and app-specific password"
	fi
}

# notary ARGS...: runs `xcrun notarytool ARGS` with AUTH, showing the output
# and keeping it in $OUT. Apple's service sometimes answers HTTP 5xx; those
# are retried with a growing pause. Returns notarytool's exit status.
notary() {
	local attempt rc
	for attempt in 1 2 3 4; do
		rc=0
		xcrun notarytool "$@" "${AUTH[@]}" 2>&1 | tee "$OUT" || rc=$?
		grep -q 'HTTP status code: 5' "$OUT" || return "$rc"
		[ "$attempt" = 4 ] && break
		echo "sign-notarize: Apple's notary service answered HTTP 5xx; retry $attempt in $((attempt * 60))s" >&2
		sleep $((attempt * 60))
	done
	echo "sign-notarize: Apple's notary service kept answering HTTP 5xx" >&2
	return 1
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
	TMPD="$(mktemp -d)"
	OUT="$TMPD/notarytool.out"
	trap 'rm -rf "$TMPD"' EXIT
	notary_auth
	if [ "${#AUTH[@]}" -gt 0 ]; then
		# Try the credentials before uploading anything, so that a wrong secret
		# or an account problem is reported as such.
		if ! notary history >/dev/null; then
			cat "$OUT" >&2
			cat >&2 <<-'EOF'
				sign-notarize: Apple refused the notarization credentials (nothing was uploaded).
				  HTTP 401: wrong Apple ID / app-specific password, or a revoked API key.
				  HTTP 403: an agreement to accept on developer.apple.com, or a Team ID
				            that this Apple ID does not belong to.
				  HTTP 5xx every time: often account-side; try an App Store Connect API key
				            (README "Releasing") instead of the Apple ID.
			EOF
			exit 1
		fi
		echo "sign-notarize: Apple accepted the notarization credentials"
		notary submit "$dmg" --wait --timeout 30m || true
		if ! grep -q 'status: Accepted' "$OUT"; then
			sub="$(awk '$1 == "id:" { print $2; exit }' "$OUT")"
			if [ -n "$sub" ] && grep -q 'status: \(Invalid\|Rejected\)' "$OUT"; then
				notary log "$sub" || true
				echo "sign-notarize: Apple rejected the dmg (see its log above)" >&2
			else
				echo "sign-notarize: notarization did not finish (see above)" >&2
			fi
			exit 1
		fi
		xcrun stapler staple "$dmg"
		xcrun stapler validate "$dmg"
		spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg"
	else
		echo "sign-notarize: no notarization secrets set; dmg signed but not notarized"
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
