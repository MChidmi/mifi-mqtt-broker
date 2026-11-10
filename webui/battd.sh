#!/system/bin/sh
# Battery charge-limiter daemon. Keeps the built-in battery between LOW and HIGH
# so it isn't held at 100% while the router is permanently on wall power.
BB=/data/local/tmp/webui/busybox
CONF=/data/local/tmp/webui/battd.conf
NODE=/sys/class/power_supply/battery/charging_enabled
CAP=/sys/class/power_supply/battery/capacity

set_chg(){ [ "$(cat $NODE 2>/dev/null)" != "$1" ] && echo "$1" > $NODE 2>/dev/null; }

while :; do
  ENABLED=0; HIGH=80; LOW=60
  [ -f "$CONF" ] && . "$CONF" 2>/dev/null
  c=$(cat $CAP 2>/dev/null); c=${c:-50}
  if [ "$ENABLED" = "1" ]; then
    if [ "$c" -ge "$HIGH" ]; then set_chg 0
    elif [ "$c" -le "$LOW" ]; then set_chg 1
    fi            # between LOW..HIGH: hold current state (hysteresis)
  else
    set_chg 1     # limiter off -> always allow charging
  fi
  $BB sleep 50
done
