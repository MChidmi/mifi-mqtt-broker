# For AI agents working in this repo

If the user wants this set up on their router, **read `docs/HOWTO.md` in full first**, especially
the section **"Instructions for AI agents"**. Then follow "Manual setup, step by step" in order.

Short version of the rules there:
- Check the device is `MIFID` / `msm8916` before anything else.
- Take the full EDL backup before any write.
- Test-boot with `fastboot boot` before `fastboot flash`.
- Ask the user before every flash write. Only the `boot` partition is ever written.
- Physical steps (USB, holding the button) are the user's job.
- Never add a web shell, and never publish the backup.
