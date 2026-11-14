#!/usr/bin/env python3
"""Turn the stock boot image of the MSM8916 MiFi into a rooted one.

    python3 tools/mkroot.py backup/boot.bin boot-root.img

What it changes (in the ramdisk only; kernel and device tree are copied byte for byte):
  1. default.prop: ro.secure=1 -> ro.secure=0 and ro.adb.secure=0 (adb shell becomes root;
     SELinux is already permissive on the stock firmware, so nothing else is needed)
  2. init.rc: appends a `webui` service that runs /data/local/tmp/webui/boot.sh at boot
     (starts the web UI, MQTT broker and battery daemon). Harmless if that file is absent.
     Pass --no-autostart to skip this.

It keeps every header field of the original image (load addresses, page size, cmdline) and
the Qualcomm device-tree blob appended after the ramdisk. Dropping that blob is the classic
mistake: the stock bootloader then refuses the image with "dtb not found".
Pure Python, no root needed: the cpio archive is edited in memory, so file owners and modes
inside the ramdisk stay exactly as they were.
"""
import argparse, gzip, hashlib, struct, sys

SERVICE = b"""
# Custom web UI auto-start (added for rooted modem)
service webui /system/bin/sh /data/local/tmp/webui/boot.sh
    class late_start
    user root
    group root
    oneshot
    seclabel u:r:init:s0
"""


def rnd(x, page):
    return (x + page - 1) // page * page


def parse_boot(d):
    if d[:8] != b"ANDROID!":
        sys.exit("not an Android boot image (missing ANDROID! magic)")
    ks, ka, rs, ra, ss, sa, tags, page, dts, _ = struct.unpack("<10I", d[8:48])
    ko = page
    ro = ko + rnd(ks, page)
    so = ro + rnd(rs, page)
    do = so + rnd(ss, page)
    return dict(hdr=d[:page], page=page, kernel=d[ko:ko + ks], ramdisk=d[ro:ro + rs],
                second=d[so:so + ss], dt=d[do:do + dts])


def qcom_id(kernel, ramdisk, second, dt):
    h = hashlib.sha1()
    for blob in (kernel, ramdisk, second):
        h.update(blob); h.update(struct.pack("<I", len(blob)))
    if dt:
        h.update(dt); h.update(struct.pack("<I", len(dt)))
    return h.digest()


def cpio_entries(data):
    """Parse a newc cpio archive into [(header_fields, name, body)]."""
    out, off = [], 0
    while True:
        if data[off:off + 6] != b"070701":
            sys.exit("ramdisk is not a newc cpio archive at offset %d" % off)
        f = [int(data[off + 6 + i * 8: off + 14 + i * 8], 16) for i in range(13)]
        namesize, filesize = f[11], f[6]
        noff = off + 110
        name = data[noff:noff + namesize - 1]
        boff = (noff + namesize + 3) & ~3
        body = data[boff:boff + filesize]
        out.append((f, name, body))
        off = (boff + filesize + 3) & ~3
        if name == b"TRAILER!!!":
            return out


def cpio_build(entries):
    out = bytearray()
    for f, name, body in entries:
        f = list(f)
        f[6] = len(body)
        f[11] = len(name) + 1
        out += b"070701" + b"".join(b"%08X" % v for v in f) + name + b"\0"
        out += b"\0" * (-len(out) % 4)
        out += body
        out += b"\0" * (-len(out) % 4)
    out += b"\0" * (-len(out) % 512)
    return bytes(out)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("stock", help="stock boot image (e.g. backup/boot.bin from the EDL backup)")
    ap.add_argument("out", help="rooted boot image to write")
    ap.add_argument("--no-autostart", action="store_true", help="only root, don't add the webui service")
    a = ap.parse_args()

    d = open(a.stock, "rb").read()
    b = parse_boot(d)
    if not b["dt"]:
        print("warning: no device-tree blob found in header (dt_size=0)")
    if qcom_id(b["kernel"], b["ramdisk"], b["second"], b["dt"]) != b["hdr"][576:596]:
        print("warning: stock image id hash doesn't match the Qualcomm layout this tool expects")

    entries = cpio_entries(gzip.decompress(b["ramdisk"]))
    done = set()
    for i, (f, name, body) in enumerate(entries):
        if name == b"default.prop":
            if b"ro.secure=0" in body:
                print("default.prop: already ro.secure=0")
            elif b"ro.secure=1" not in body:
                sys.exit("default.prop has no ro.secure=1 line - unexpected firmware, stopping")
            body = body.replace(b"ro.secure=1", b"ro.secure=0")
            if b"ro.adb.secure=" not in body:      # no adb key prompt (matches the tested image)
                body = body.rstrip(b"\n") + b"\nro.adb.secure=0\n"
            done.add("prop")
        elif name == b"init.rc" and not a.no_autostart:
            if b"service webui " in body:
                print("init.rc: webui service already present")
            else:
                body = body.rstrip(b"\n") + b"\n" + SERVICE
            done.add("rc")
        entries[i] = (f, name, body)
    if "prop" not in done:
        sys.exit("default.prop not found in ramdisk")
    if not a.no_autostart and "rc" not in done:
        sys.exit("init.rc not found in ramdisk")

    ramdisk = gzip.compress(cpio_build(entries), 9, mtime=0)
    page = b["page"]
    hdr = bytearray(b["hdr"])
    struct.pack_into("<I", hdr, 16, len(ramdisk))
    hdr[576:608] = qcom_id(b["kernel"], ramdisk, b["second"], b["dt"]).ljust(32, b"\0")

    def pad(x):
        return x + b"\0" * (rnd(len(x), page) - len(x))
    img = bytes(hdr) + pad(b["kernel"]) + pad(ramdisk) + pad(b["second"]) + pad(b["dt"])
    open(a.out, "wb").write(img)
    print("wrote %s (%d bytes): kernel %d, ramdisk %d -> %d, dtb %d bytes kept"
          % (a.out, len(img), len(b["kernel"]), len(b["ramdisk"]), len(ramdisk), len(b["dt"])))
    print("changes: ro.secure=0, ro.adb.secure=0" + ("" if a.no_autostart else " + webui autostart service"))


if __name__ == "__main__":
    main()
