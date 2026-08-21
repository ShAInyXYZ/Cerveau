#!/usr/bin/env bash
# Rebuild the Cerveau phone APK (portal + panel shell).
# Requires: JDK 17+, Android SDK (platforms + build-tools) in $ANDROID_HOME or ~/Android/Sdk.
set -euo pipefail
cd "$(dirname "$0")"

SDK="${ANDROID_HOME:-$HOME/Android/Sdk}"
BT="$(ls -d "$SDK"/build-tools/* | sort -V | tail -1)"
AJ="$(ls -d "$SDK"/platforms/android-*/android.jar | sort -V | tail -1)"

rm -rf classes classes.dex base.apk unsigned.apk aligned.apk res.zip gen
mkdir -p classes gen

"$BT/aapt2" compile --dir res -o res.zip
# --java emits R.java so code can reference @drawable/@mipmap resources
"$BT/aapt2" link -o base.apk -I "$AJ" --manifest AndroidManifest.xml --java gen res.zip
# vendor/ holds the decode-only ZXing subset (Apache-2.0) so the pairing QR
# is read IN-APP — never handed to a third-party scanner.
javac --release 17 -nowarn -cp "$AJ" -d classes $(find src gen vendor -name '*.java')
"$BT/d8" --release --lib "$AJ" --min-api 29 --output . $(find classes -name '*.class')
cp base.apk unsigned.apk
zip -q unsigned.apk classes.dex
"$BT/zipalign" -f 4 unsigned.apk aligned.apk

# keystore: keep your own — this generates a throwaway one if absent.
# The signing password is NEVER hardcoded. Provide it via $KS_PASS. For the
# auto-generated throwaway keystore we mint a random password and stash it in
# the gitignored cerveau.keystore.pass so re-signing the same keystore works.
PASS_FILE="cerveau.keystore.pass"
if [ ! -f cerveau.keystore ]; then
  KS_PASS="${KS_PASS:-$(head -c 24 /dev/urandom | base64 | tr -d '/+=' | head -c 32)}"
  printf '%s' "$KS_PASS" > "$PASS_FILE"
  chmod 600 "$PASS_FILE"
  keytool -genkeypair -keystore cerveau.keystore -alias cerveau \
    -keyalg RSA -keysize 2048 -validity 10950 \
    -storepass "$KS_PASS" -keypass "$KS_PASS" -dname "CN=Cerveau,O=shiny" 2>/dev/null
fi
# Resolve the password: explicit env wins; else the stashed throwaway password.
if [ -z "${KS_PASS:-}" ] && [ -f "$PASS_FILE" ]; then
  KS_PASS="$(cat "$PASS_FILE")"
fi
if [ -z "${KS_PASS:-}" ]; then
  echo "error: KS_PASS is unset and no $PASS_FILE exists — export KS_PASS to sign." >&2
  exit 1
fi
"$BT/apksigner" sign --ks cerveau.keystore --ks-pass "pass:$KS_PASS" --out Cerveau.apk aligned.apk
echo "built android/Cerveau.apk"
