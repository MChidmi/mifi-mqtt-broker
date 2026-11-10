# How it works & how to build it yourself (no Claude needed)

I built this by talking to [Claude Code](https://claude.com/claude-code), but there's no magic in
it — here's the actual recipe so you can redo it by hand. Fair warning: the **exact** commands
depend on your specific router. The shapes below are what worked on this one (Qualcomm MSM8916 /
Android 4.4). Adapt, don't copy blindly, and **back everything up first.**

## Why Go for the broker (and not mosquitto)

The obvious choice was mosquitto, but the ready-made mosquitto builds won't run on this router —
it's an Android device and its system libraries don't match the ones those builds expect. Building
mosquitto to fit would've meant setting up a whole cross-compiler and rebuilding its dependencies.

Go sidesteps all of that: it cross-compiles to a **single self-contained binary** that runs on the
router as-is. The broker itself is [mochi-mqtt](https://github.com/mochi-mqtt/server), wrapped in a
tiny `main.go`. That's the whole reason — not a preference, just the path that actually worked.

## How it's put together

Everything lives in `/data/local/tmp/webui/` on the router (survives reboots; the stock system
partition is left alone):

- A small static **BusyBox `httpd`** serves the web UI and proxies API calls to the router's existing
  service, so the new UI is same-origin with no CORS fuss and the stock backend stays untouched.
- The UI's root-powered endpoints (CGI scripts) each **validate the router's login session** before
  doing anything, so they aren't open to the network.
- Port 80 is pointed at the new UI via a firewall rule; the stock UI stays reachable on its own port.
- A boot hook (an init service added to a rooted boot image) starts the web server, the broker, and
  the battery daemon on every boot.

```
webui/
  www/index.html            # the whole UI, one vanilla-JS file (no framework)
  www/cgi-bin/              # api (proxy), sms, bat, battctl, led, mqtt, sysinfo
  boot.sh                   # runs at boot: start httpd + broker + battery daemon, fix firewall
  battd.sh / battd.conf     # battery charge limiter
  start.sh / httpd.conf     # manual start + httpd config
broker/
  main.go / go.mod / go.sum # the MQTT broker (mochi-mqtt)
```

## MQTT notification LED

The router's charge LED is actually two LEDs — `bat1` (red) and `batl2` (green) under
`/sys/class/leds/`. The broker repurposes them:

- **flashes red** whenever a message passes through the broker — either direction (a client
  publishes in via `OnPublish`, or the broker delivers out via `OnPublished`);
- between flashes it's **green** while at least one client is connected, **off** when none;
- publish to the topic **`led`** to override the base colour: `green` / `red` / `off` / `auto`;
- it never lights both channels at once (no yellow).

A fast ticker (~60 ms) reads an enable flag and rewrites the colour, so the firmware's own
battery-charge driver can't steal the LEDs back. A web toggle (`cgi-bin/ledcfg` → `led.conf`
`ENABLED=0/1`) turns the whole thing on/off — **off** hands the LEDs back to the normal battery
indication. The two paths, the topic, and the config file are overridable with the `LED_RED`,
`LED_GREEN`, `LED_TOPIC`, and `LED_CONF` env vars. It's all in `broker/main.go` (a small mochi hook).

```sh
# watch it flash red as messages flow:
mosquitto_pub -h <router-ip> -t anything -m hi
# force a colour / hand it back:
mosquitto_pub -h <router-ip> -t led -m red
mosquitto_pub -h <router-ip> -t led -m auto
```

## Reproduce it yourself

### 0. What you need

- Root on the router (an `adb root` shell). This is the one hard prerequisite — everything else
  builds on it.
- A static **ARM BusyBox that includes `httpd`** pushed to the device (I used the
  `defconfig-multiarch` build from [busybox.net/downloads/binaries](https://busybox.net/downloads/binaries/)).
  BusyBox gives you `httpd`, `nc`, a real shell, etc. that the stock Android toolbox lacks.

### 1. Get root

This router made it easy: its bootloader boots **unsigned** images (`fastboot boot some.img` works)
and SELinux already runs **permissive**, so root was just flipping one flag in the boot ramdisk:

- Back up first. I dumped every partition over **EDL** using a MSM8916 "firehose" loader (the one
  from Linaro's DragonBoard-410c rescue package) so I could always restore — do the equivalent for
  your board before writing anything.
- Pull the stock **boot** partition and unpack it. An Android boot image = kernel + ramdisk + (on
  Qualcomm) an **appended device-tree blob**. Keep that DTB — repack without it and the bootloader
  rejects the image with `dtb not found`.
- In the ramdisk's `default.prop`, change `ro.secure=1` → `ro.secure=0`. With SELinux permissive,
  that alone makes `adb` give you a root shell.
- Repack with the **same load addresses** and try it with `fastboot boot newboot.img` first — that's
  zero-risk (it reverts on reboot). Once it works, `fastboot flash boot newboot.img`.
- Your device may differ — yours might need Magisk, an unlocked bootloader, or a different trick.
  That's the one genuinely device-specific step.

### 2. Serve the web UI

- Push `webui/www/` and the static busybox to `/data/local/tmp/webui/` and run:
  `busybox httpd -p 8080 -h /data/local/tmp/webui/www`.
- The stock router already has a web backend with a JSON API. The new UI talks to it through a tiny
  CGI proxy (`cgi-bin/api`) that forwards each request to the stock server with `nc` and strips its
  chunked-encoding framing — so the page is **same-origin** (no CORS) and the stock backend is left
  running untouched. The root-powered CGIs (`sms`, `bat`, `battctl`, `led`, `mqtt`, `sysinfo`) each
  check the stock login session before doing anything.
- Point port 80 at the new UI with an iptables DNAT rule (see `webui/boot.sh`); the stock UI stays on
  its own port as a fallback.

### 3. Build + run the MQTT broker

`broker/main.go` is a ~30-line wrapper around [mochi-mqtt](https://github.com/mochi-mqtt/server).
Cross-compile it (no cross-toolchain needed — this is the whole reason for Go):

```sh
cd broker
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags "-s -w" -o mqttbroker .
```

Push `mqttbroker` to the device, run it (`./mqttbroker :1883 :9001`), and open those ports in the
firewall (`iptables -I INPUT 1 -p tcp --dport 1883 -j ACCEPT`, same for `9001`).

### 4. Battery "don't cook yourself" limiter

A lithium battery kept at 100% all the time ages faster and can puff up. This router is permanently
plugged into the wall, so left alone it would sit at 100% forever. The fix: let it bounce between two
levels — stop charging when it's "full enough", let it drain a bit, then top it back up.

On this router (Qualcomm PMIC) there's a kernel switch that turns the charger on and off:

```
/sys/class/power_supply/battery/charging_enabled      # write 0 = stop charging, 1 = charge
/sys/class/power_supply/battery/capacity              # current % (read-only)
```

`webui/battd.sh` just loops: read the %, write `0` at/above the **stop** level, write `1` at/below
the **resume** level, otherwise leave it alone (so it doesn't flap). The web UI's "Battery Care" card
writes those two numbers into a config file the daemon reads.

**To do it on any rooted device, no web UI needed:**

1. Find your charge switch — not every device names it the same:

   ```sh
   ls /sys/class/power_supply/battery/ | grep -iE 'charg|input_suspend|charge_control'
   ```

   Common names: `charging_enabled` (0/1), `input_suspend` (1 = pause, 0 = charge — note it's
   inverted), or `charge_control_limit`. Test it: `echo 0 > .../charging_enabled` and watch
   `cat .../status` change from `Charging` to `Not charging`/`Full`. Put it back with `echo 1`.

2. Drop in this loop (adjust the path + the two numbers), run it as root:

   ```sh
   #!/system/bin/sh
   NODE=/sys/class/power_supply/battery/charging_enabled   # your switch from step 1
   CAP=/sys/class/power_supply/battery/capacity
   HIGH=80   # stop charging at this %
   LOW=60    # resume charging at this %
   while :; do
     c=$(cat "$CAP"); cur=$(cat "$NODE")
     if   [ "$c" -ge "$HIGH" ] && [ "$cur" != 0 ]; then echo 0 > "$NODE"
     elif [ "$c" -le "$LOW"  ] && [ "$cur" != 1 ]; then echo 1 > "$NODE"
     fi
     sleep 50
   done
   ```

3. Start it in the background (`setsid sh battery.sh &`) and launch it at boot (init service, an
   `init.d` script, a cron `@reboot`, or Tasker). The only device-specific part is the filename in
   step 1 — the rest is the same loop everywhere.

### 5. Make it all survive reboots

Start the web server, the broker, and the battery daemon from whatever runs at boot on your device.
Here that's an **init service** added to the rooted boot image, which runs `webui/boot.sh` (it
launches everything and re-applies the firewall/port rules). On other setups an `init.d` script, a
cron `@reboot`, or Tasker would do the same job. Everything lives under `/data/local/tmp/webui/`, so
the stock system partition is never modified.

The short, commented scripts in this repo (`webui/boot.sh`, `webui/www/cgi-bin/*`, `broker/main.go`)
*are* the documentation for the fiddly bits — read them.
