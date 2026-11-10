#!/system/bin/sh
# Auto-start the custom web UI and point port 80 at it. Runs at boot (init service).
BB=/data/local/tmp/webui/busybox
D=/data/local/tmp/webui
R80="-d 192.168.100.1/32 -p tcp -m tcp --dport 80 -j DNAT --to-destination"

# start the UI server on :8080 if not already up
if ! $BB pgrep -f "httpd -p 8080" >/dev/null 2>&1; then
  $BB chmod 755 $D/busybox $D/www/cgi-bin/* 2>/dev/null
  $BB httpd -p 8080 -h $D/www -c $D/httpd.conf
fi

# start the battery charge-limiter daemon (reads battd.conf; off by default)
$BB pgrep -f battd.sh >/dev/null 2>&1 || $BB setsid /system/bin/sh $D/battd.sh >/dev/null 2>&1 &

# start the MQTT broker for local devices (if the binary is present)
if [ -x "$D/mqttbroker" ]; then
  $BB pgrep -f mqttbroker >/dev/null 2>&1 || $BB setsid "$D/mqttbroker" ":1883" ":9001" >/dev/null 2>&1 &
  for port in 1883 9001; do
    iptables -C INPUT -p tcp --dport $port -j ACCEPT 2>/dev/null || iptables -I INPUT 1 -p tcp --dport $port -j ACCEPT 2>/dev/null
  done
fi

# Enforce port 80 -> our UI (8080) for ~4 min, since the stock 80->8000 rule is
# installed during boot and can reappear late. -I keeps ours first; we drop 8000.
i=0
while [ $i -lt 120 ]; do
  iptables -t nat -C oem_nat_pre $R80 192.168.100.1:8000 2>/dev/null && \
    iptables -t nat -D oem_nat_pre $R80 192.168.100.1:8000 2>/dev/null
  iptables -t nat -C oem_nat_pre $R80 192.168.100.1:8080 2>/dev/null || \
    iptables -t nat -I oem_nat_pre 1 $R80 192.168.100.1:8080 2>/dev/null
  i=$((i+1)); sleep 2
done
