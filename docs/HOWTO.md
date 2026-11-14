# How it works & how to set it up yourself

There are two ways to set this up:

1. **[Manual setup, step by step](#manual-setup-step-by-step)**: every command in order, so you can
   copy and paste your way from a stock router to a running broker. Rooting comes first.
2. **[With Claude Code or another AI agent](#with-claude-code-or-another-ai-agent)**: paste one
   prompt and let the agent run the steps. It stops and asks you whenever something physical or
   risky has to happen.

> **Which router is this for?** A Qualcomm **MSM8916** 4G MiFi running **Android 4.4** that reports
> itself as `MIFID` (sold under names like HiMILink). Step 1 checks this. On a different device the
> rooting steps won't match, so stop there. Everything is at your own risk, and the backup in
> step 2 is not optional.

---

## Manual setup, step by step

The commands are for a **Linux PC** (Debian/Ubuntu; other distros only differ in the package
manager). Run them from a normal user account; `sudo` is only used where shown.

### Step 0: Install the tools

**Packages** (adb, fastboot, Python, git):

```sh
sudo apt update
sudo apt install -y adb fastboot python3 python3-venv git unzip curl
```

**USB permissions**, so adb, fastboot and EDL mode work without root:

```sh
echo 'SUBSYSTEM=="usb", ATTR{idVendor}=="05c6", MODE="0666"
SUBSYSTEM=="usb", ATTR{idVendor}=="18d1", MODE="0666"' | sudo tee /etc/udev/rules.d/51-mifi.rules
sudo udevadm control --reload-rules && sudo udevadm trigger
```

**Go**, to build the MQTT broker. Use `linux-arm64` instead of `linux-amd64` on an ARM PC:

```sh
curl -LO https://go.dev/dl/go1.23.5.linux-amd64.tar.gz
mkdir -p ~/sdk && tar -C ~/sdk -xzf go1.23.5.linux-amd64.tar.gz
export PATH=$HOME/sdk/go/bin:$PATH     # add this line to ~/.bashrc to keep it
go version                             # -> go version go1.23.5 ...
```

**edl** (bkerler's Qualcomm EDL tool), used for the full backup:

```sh
git clone --recursive https://github.com/bkerler/edl ~/edl
python3 -m venv ~/edl/venv
grep -v keystone ~/edl/requirements.txt > /tmp/edl-req.txt   # keystone isn't needed and often fails to build
~/edl/venv/bin/pip install -r /tmp/edl-req.txt
```

**The MSM8916 "firehose" loader** (from Linaro's DragonBoard 410c rescue package). EDL needs it
to read the flash:

```sh
mkdir -p ~/mifi-backup && cd ~/mifi-backup
curl -LO https://github.com/boardport/linaro-releases/releases/download/db410c-rescue-21.12/dragonboard-410c-bootloader-emmc-linux-176.zip
unzip -j dragonboard-410c-bootloader-emmc-linux-176.zip '*prog_emmc_firehose_8916.mbn'
md5sum prog_emmc_firehose_8916.mbn     # must be e7c061dbab111c311c365fe9ce81010a
```

**This repo:**

```sh
cd ~ && git clone https://github.com/MChidmi/mifi-mqtt-broker
cd ~/mifi-mqtt-broker
```

From here on, every command runs from `~/mifi-mqtt-broker`.

### Step 1: Connect and check it's the right router

Plug the router into the PC with a **USB data cable** (it shows up as an adb device even when
stock).

```sh
adb devices                              # one device listed as "device"
adb shell getprop ro.product.model       # -> MIFID
adb shell getprop ro.board.platform      # -> msm8916
adb shell getprop ro.build.version.release  # -> 4.4.4
adb shell id                             # -> uid=2000(shell) ... (not root yet)
```

If the model isn't `MIFID` or the platform isn't `msm8916`, **stop here**: the rest of this guide
is for that board only.

(No USB data connection? The stock firmware also has adb open over Wi-Fi:
`adb connect 192.168.100.1:22345`. You still need USB for fastboot and EDL.)

### Step 2: Back up every partition (EDL mode)

This is your undo button. With this backup, even a badly broken flash can be restored.

1. **Unplug** the router from USB. If it's on, power it off.
2. **Hold the router's button** and, while holding it, **plug the USB cable in**. Keep holding for
   about 3 seconds, then let go.
3. Check that it's in EDL mode. The router shows **no lights** in this mode, which is normal:

   ```sh
   lsusb | grep 05c6:9008     # -> ... Qualcomm ... QDL mode
   ```

   No `05c6:9008`? Unplug and try step 2 again. If ModemManager is installed and keeps grabbing
   the device, run `sudo systemctl stop ModemManager` first.

4. Read the partition table, then dump everything except the big user-data partitions:

   ```sh
   LOADER=~/mifi-backup/prog_emmc_firehose_8916.mbn
   ~/edl/venv/bin/python ~/edl/edl.py --loader $LOADER printgpt
   ~/edl/venv/bin/python ~/edl/edl.py --loader $LOADER rl ~/mifi-backup/edl --skip=system,userdata,cache
   ls -la ~/mifi-backup/edl/      # ~30 .bin files: boot.bin, aboot.bin, modem.bin, ...
   ```

5. Check that the boot backup looks right, then reboot back to Android:

   ```sh
   head -c 8 ~/mifi-backup/edl/boot.bin; echo     # -> ANDROID!
   ~/edl/venv/bin/python ~/edl/edl.py --loader $LOADER reset
   ```

**Copy `~/mifi-backup` somewhere safe** (another disk or a cloud drive). Never publish it: it
contains this unit's IMEI and calibration data.

### Step 3: Build the rooted boot image

```sh
python3 tools/mkroot.py ~/mifi-backup/edl/boot.bin boot-root.img
```

`tools/mkroot.py` changes only the ramdisk:

- In `default.prop`: `ro.secure=0` and `ro.adb.secure=0`, so adb becomes root. SELinux is already
  permissive on this firmware, so nothing else is needed.
- In `init.rc`: one `webui` service that runs `/data/local/tmp/webui/boot.sh` at every boot. That
  file doesn't exist yet, so this does nothing until step 8.

The kernel, load addresses, command line and the Qualcomm device-tree blob are copied unchanged.
That blob matters: without it the bootloader says `dtb not found`.

### Step 4: Test-boot it from RAM (nothing written yet)

```sh
adb reboot bootloader
sleep 8 && fastboot devices          # one device listed
fastboot boot boot-root.img          # boots the image from RAM, flash untouched
adb wait-for-device && sleep 5
adb shell id                         # -> uid=0(root)  <- it works
```

Not `uid=0`? Power-cycle the router: it comes back exactly as before, because nothing was
written. Don't continue.

### Step 5: Flash it permanently

```sh
adb reboot bootloader
sleep 8 && fastboot flash boot boot-root.img
fastboot reboot
adb wait-for-device && sleep 5
adb shell id                         # -> uid=0(root), now on every boot
```

### Step 6: Get BusyBox (web server and shell tools for the router)

```sh
curl -L -o tools/busybox-armv7l https://busybox.net/downloads/binaries/1.31.0-defconfig-multiarch-musl/busybox-armv7l
md5sum tools/busybox-armv7l          # must be 10ac2a1c34b0b02f2d0ce7a4420ade5c
```

### Step 7: Build the MQTT broker

```sh
cd broker
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o mqttbroker .
file mqttbroker                      # -> ELF 32-bit LSB executable, ARM, ... statically linked
cd ..
```

### Step 8: Install everything on the router

```sh
sh tools/install.sh
```

This copies the web UI, BusyBox, the broker and the battery daemon to `/data/local/tmp/webui/`,
then starts them. It ends with:

```
  port 8080: listening
  port 1883: listening
  port 9001: listening
== done: open http://192.168.100.1/ (stock UI stays on :8000)
```

### Step 9: Use it

- Join the router's Wi-Fi and open **http://192.168.100.1/**. Sign in with the same username and
  password as the stock page. The old stock page stays available at http://192.168.100.1:8000/.
- Test the broker from any machine on the Wi-Fi (`sudo apt install mosquitto-clients`):

  ```sh
  mosquitto_sub -h 192.168.100.1 -t '#' -v &
  mosquitto_pub -h 192.168.100.1 -t test -m hello      # -> "test hello", and the LED flashes red
  ```

### Step 10: Check it survives a reboot

```sh
adb reboot
adb wait-for-device && sleep 30
adb shell "/data/local/tmp/webui/busybox netstat -ltn | grep -E ':(8080|1883|9001) '"   # three lines
```

Port 80 switches to the new UI about 20 seconds after boot.

### Updating, undoing, recovering

```sh
# update to the latest version of this repo
git pull && (cd broker && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o mqttbroker .) && sh tools/install.sh

# remove the UI + broker but keep root (stock UI is back on port 80 after a reboot)
adb shell rm -rf /data/local/tmp/webui && adb reboot

# un-root completely: put the original boot image back
adb reboot bootloader
sleep 8 && fastboot flash boot ~/mifi-backup/edl/boot.bin && fastboot reboot

# router won't boot at all: enter EDL (step 2: hold button + plug in) and write the backup back
LOADER=~/mifi-backup/prog_emmc_firehose_8916.mbn
~/edl/venv/bin/python ~/edl/edl.py --loader $LOADER w boot ~/mifi-backup/edl/boot.bin
~/edl/venv/bin/python ~/edl/edl.py --loader $LOADER reset
```

---

## With Claude Code or another AI agent

If you'd rather have an agent do the typing:

1. Do the **physical prerequisites** yourself: a Linux PC, the router plugged in by USB data cable.
2. Install [Claude Code](https://claude.com/claude-code), or use another coding agent that can run
   shell commands.
3. Open a terminal and start the agent in an empty folder (`mkdir mifi && cd mifi && claude`).
4. Paste this prompt:

```text
Set up https://github.com/MChidmi/mifi-mqtt-broker on my 4G MiFi router, which is plugged into
this PC by USB. Clone the repo, then read docs/HOWTO.md completely, including the section
"Instructions for AI agents", and follow it. Work through the manual setup steps in order,
running the commands yourself. Stop and tell me exactly what to do whenever a step needs my
hands (unplugging, holding the button) and ask me before anything that writes to the router's
flash. When it's done, show me the proof: root, the three listening ports, the web page, and an
MQTT publish/subscribe test.
```

The agent stops and asks you before the backup, before flashing, and whenever you need to press
the button. You approve each step.

### Instructions for AI agents

*If you are an AI agent and a user asked you to set up this repo, these rules apply to you.*

**Goal:** a rooted MIFID/MSM8916 router running the web UI on port 80 (served on 8080), the MQTT
broker on 1883/9001, and the battery daemon, all surviving a reboot. Follow
[Manual setup, step by step](#manual-setup-step-by-step) in order. The commands there are tested;
don't improvise alternatives.

**Hard rules:**

- **Verify the device first (step 1).** If `ro.product.model` isn't `MIFID` or
  `ro.board.platform` isn't `msm8916`, stop and explain. Don't adapt the rooting steps to another
  device unless the user explicitly asks you to, understanding the risk.
- **Backup before any write.** Don't build or flash anything until `~/mifi-backup/edl/` holds the
  full dump and `boot.bin` starts with `ANDROID!`. Tell the user to copy that folder somewhere safe.
- **Always test-boot (step 4) before flashing (step 5).** Continue only if `adb shell id` shows
  `uid=0` from the RAM-booted image.
- **Ask the user before each flash write** (`fastboot flash`, `edl ... w`). The only partition this
  guide ever writes is **boot**. Never write `aboot`, `sbl1`, `tz`, `hyp`, `rpm`, `modem*`, `fsg`,
  `persist`, the GPT, or anything else.
- **Physical steps belong to the user.** Plugging and unplugging USB and holding the button for EDL
  need a human. Give clear instructions, then wait for them to confirm. Don't loop forever on
  `lsusb`; give up after about 2 minutes and ask.
- **Don't add a web shell** or any endpoint that runs arbitrary commands. Every root CGI in
  `webui/www/cgi-bin/` checks the router's login session first; keep it that way in any change.
- **Never publish or upload the backup**: it holds the IMEI and RF calibration.
- If something fails twice the same way, stop and report it with the exact output instead of
  trying riskier things.

**Device quirks that will otherwise waste your time:**

- `adb shell` on Android 4.4 **does not return the remote exit code**. Check printed output
  instead (e.g. `adb shell "[ -f X ] && echo yes" | grep -q yes`).
- The stock shell lacks `head`, `tail`, `sort`, `printf` and others. Use
  `/data/local/tmp/webui/busybox <cmd>` once step 8 has pushed it.
- Starting a background process over adb: `adb shell "busybox setsid cmd </dev/null >/dev/null 2>&1 & sleep 1; echo started"`.
  Without the `sleep 1`, adb closes the session and kills it before it detaches.
- `pgrep -f`/`pkill -f` match their own `sh -c` command line. Use bracket patterns like
  `'[m]qttbroker'`.
- `fastboot` needs about 8 seconds after `adb reboot bootloader` to show up.
- After `fastboot boot` or a reboot, Android takes 30–40 seconds before adb answers.
- The stock web backend (`himiwebserver`, JSON API at `POST /himiapi/json`) keeps running on
  port 8000. The new UI proxies to it through `cgi-bin/api`. Don't stop or replace it.

**Done when** you have shown the user all of these:

- `adb shell id` → `uid=0`
- `tools/install.sh` printed three `listening` lines
- `adb shell "iptables -t nat -S oem_nat_pre"` contains `--dport 80 ... 192.168.100.1:8080`
- An MQTT publish/subscribe round trip works, if the PC is on the router's Wi-Fi
- The same ports are listening again after `adb reboot`

---

## How it works

### Why Go for the broker (and not mosquitto)

The obvious choice was mosquitto, but the ready-made mosquitto builds won't run on this router.
It's an Android device, and its system libraries don't match the ones those builds expect.
Building mosquitto to fit would have meant setting up a whole cross-compiler and rebuilding its
dependencies.

Go sidesteps all of that: it cross-compiles to a **single self-contained binary** that runs on the
router as-is. The broker itself is [mochi-mqtt](https://github.com/mochi-mqtt/server), wrapped in a
small `main.go`. That's the whole reason. It isn't a preference; it's the path that actually worked.

### How it's put together

Everything lives in `/data/local/tmp/webui/` on the router. That folder survives reboots, and the
stock system partition is left alone.

- A small static **BusyBox `httpd`** serves the web UI and proxies API calls to the router's
  existing service. The new UI is same-origin, so there's no CORS fuss, and the stock backend stays
  untouched.
- The UI's root-powered endpoints (CGI scripts) each **check the router's login session** before
  doing anything, so they aren't open to the network.
- Port 80 is pointed at the new UI by a firewall rule. The stock UI stays reachable on port 8000.
- A boot hook (the init service `mkroot.py` adds to the boot image) starts the web server, the
  broker and the battery daemon on every boot.

```
tools/
  mkroot.py                 # stock boot.img -> rooted boot.img (+ autostart service)
  install.sh                # push + (re)start everything over adb
webui/
  www/index.html            # the whole UI, one vanilla-JS file (no framework)
  www/cgi-bin/              # api (proxy), sms, bat, battctl, ledcfg, mqtt, sysinfo
  boot.sh                   # runs at boot: start httpd + broker + battery daemon, fix firewall
  battd.sh / battd.conf     # battery charge limiter
  start.sh / httpd.conf     # manual start + httpd config
broker/
  main.go / go.mod / go.sum # the MQTT broker (mochi-mqtt)
```

### MQTT notification LED

The router's charge LED is actually two LEDs under `/sys/class/leds/`: `bat1` (red) and `batl2`
(green). The broker repurposes them:

- It **flashes red** whenever a message passes through the broker in either direction: a client
  publishes in (`OnPublish`), or the broker delivers out (`OnPublished`).
- Between flashes it's **green** while at least one client is connected, and **off** when none are.
- Publish to the topic **`led`** to override the base colour: `green` / `red` / `off` / `auto`.
- It never lights both colours at once (no yellow).

A fast ticker (about 60 ms) reads an enable flag and rewrites the colour, so the firmware's own
battery-charge driver can't take the LEDs back. The web toggle in the Root tab (`cgi-bin/ledcfg`,
which writes `ENABLED=0/1` to `led.conf`) turns the whole thing on or off. **Off** hands the LEDs
back to the normal battery indication. The `LED_RED`, `LED_GREEN`, `LED_TOPIC` and `LED_CONF`
environment variables override the two paths, the topic and the config file.

```sh
mosquitto_pub -h 192.168.100.1 -t anything -m hi   # watch it flash red
mosquitto_pub -h 192.168.100.1 -t led -m red       # force a colour
mosquitto_pub -h 192.168.100.1 -t led -m auto      # hand it back
```

### Battery "don't cook yourself" limiter

A lithium battery kept at 100% all the time ages faster and can swell. This router is permanently
plugged into the wall, so left alone it would sit at 100% forever. The fix is to let the charge
move between two levels: stop charging when it's full enough, let it drain a bit, then top it
back up.

On this router (Qualcomm PMIC), a kernel switch turns the charger on and off:

```
/sys/class/power_supply/battery/charging_enabled      # write 0 = stop charging, 1 = charge
/sys/class/power_supply/battery/capacity              # current % (read-only)
```

`webui/battd.sh` loops: it reads the percentage, writes `0` at or above the **stop** level,
writes `1` at or below the **resume** level, and otherwise leaves the switch alone so it doesn't
flip back and forth. It's off by default. Turn it on in **Settings → Battery Care**, which writes
`battd.conf`.

**To do it on any other rooted Android device** (no web UI needed):

1. Find the charge switch. Not every device names it the same:

   ```sh
   ls /sys/class/power_supply/battery/ | grep -iE 'charg|input_suspend|charge_control'
   ```

   Common names are `charging_enabled` (0/1), `input_suspend` (1 = pause, 0 = charge; note it's
   inverted) and `charge_control_limit`. To test it, run `echo 0 > .../charging_enabled` and watch
   `cat .../status` change from `Charging` to `Not charging`. Put it back with `echo 1`.

2. Run this loop as root (adjust the path and the two numbers):

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

3. Start it in the background (`setsid sh battery.sh &`) and launch it at boot with whatever your
   device offers: an init service, an `init.d` script, a cron `@reboot`, or Tasker.
