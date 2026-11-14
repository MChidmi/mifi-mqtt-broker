#!/bin/sh
# Install (or update) the web UI, MQTT broker and battery daemon on a rooted MiFi over adb.
#   sh tools/install.sh
# Needs: rooted adb (`adb shell id` -> uid=0), tools/busybox-armv7l, broker/mqttbroker.
set -e
cd "$(dirname "$0")/.."
D=/data/local/tmp/webui

[ -f tools/busybox-armv7l ] || { echo "missing tools/busybox-armv7l (see docs/HOWTO.md step 6)"; exit 1; }
[ -f broker/mqttbroker ]    || { echo "missing broker/mqttbroker (see docs/HOWTO.md step 7)"; exit 1; }

adb wait-for-device
adb shell id | grep -q 'uid=0' || { echo "adb shell is not root yet - do the rooting steps first"; exit 1; }

echo "== pushing files to $D"
adb shell "mkdir -p $D/www/cgi-bin"
adb push tools/busybox-armv7l "$D/busybox"
adb push broker/mqttbroker   "$D/mqttbroker"
adb push webui/www/index.html "$D/www/index.html"
adb push webui/www/cgi-bin/. "$D/www/cgi-bin/"
for f in boot.sh start.sh battd.sh httpd.conf; do adb push "webui/$f" "$D/$f"; done
# keep the user's battery settings if they already exist
# (old adb doesn't return exit codes, so test the printed output instead)
adb shell "[ -f $D/battd.conf ] && echo have" | grep -q have || adb push webui/battd.conf "$D/battd.conf"
adb shell "chmod 755 $D/busybox $D/mqttbroker $D/*.sh $D/www/cgi-bin/*"

echo "== (re)starting services"
# stop old copies ([x]yz patterns so pkill doesn't match its own command line)
for pat in '[b]oot.sh' '[b]attd.sh' 'httpd -p [8]080' '[m]qttbroker'; do
  adb shell "$D/busybox pkill -f '$pat'" || true
done
# start boot.sh detached; the short sleep lets setsid detach before adb closes the session
timeout 15 adb shell "$D/busybox setsid /system/bin/sh $D/boot.sh < /dev/null > /dev/null 2>&1 & sleep 1; echo started" || true
sleep 5
L=$(adb shell "$D/busybox netstat -ltn")
for p in 8080 1883 9001; do
  echo "$L" | grep -q ":$p " && echo "  port $p: listening" || { echo "  port $p: NOT listening - rerun this script or reboot the router"; FAIL=1; }
done
[ -z "$FAIL" ] || exit 1
echo "== done: open http://192.168.100.1/ (stock UI stays on :8000)"
