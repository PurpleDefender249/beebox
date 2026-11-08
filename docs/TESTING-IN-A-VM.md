# Testing BeeBox in a virtual machine

Crashing a computer on purpose is best done in a virtual machine. This guide
works with any distribution that uses systemd (Ubuntu, Debian, Fedora, Kali,
Arch…) and any hypervisor (VMware Workstation, VirtualBox, GNOME Boxes,
virt-manager, Hyper-V).

## 1. Set up the VM

1. Create a VM with 2 CPUs, **4 GB of RAM** and a 25 GB disk.
   To test kernel crashes (section 4) the kernel must be able to save a
   crash log. **UEFI firmware** does that with no setup: in VMware
   Workstation choose *VM Settings → Options → Advanced → Firmware type →
   UEFI*, in VirtualBox *Settings → System → Enable EFI*, before installing
   (changing it later usually stops the VM booting). A VM that already uses
   **legacy BIOS** can use kdump instead; section 4 shows how.
2. Install the distribution, log in, open a terminal and copy the package
   into the VM (shared folder, drag and drop, or download it from the
   releases page).
3. Install it. If you tested an earlier build, purge it first so its old
   reports don't mix with the new ones (purging deletes BeeBox's recordings
   and reports, nothing else):

   ```sh
   sudo apt purge -y beebox                   # only if an earlier build is installed
   sudo apt install ./beebox_*_amd64.deb      # Debian, Ubuntu, Kali, Mint
   sudo dnf install ./beebox-*.x86_64.rpm     # Fedora
   ```

   Once a release is published you can use the one-line installer instead:
   `curl -fsSL https://purpledefender249.github.io/beebox/install.sh | sh`.
4. Restart the VM once, log in, and check:

   ```sh
   beebox status
   beebox doctor
   ```

   `beebox status` should say **BeeBox is recording**. Note what
   `beebox doctor` says on the **Kernel crash logs** line; it decides what
   sections 2 and 4 will report.
5. Wait two minutes before the first test, and run **one test per boot**.
   After each test, power the VM on, log in, wait for the notification, then
   record the result:

   ```sh
   beebox last --plain
   sudo beebox dump --last 150 --plain > ~/dump.txt
   ```

## 2. Test a power cut

1. Wait at least 30 seconds so there is something recorded.
2. Cut the power to the VM:
   - VMware: *VM → Power → Power Off* (not *Shut Down Guest*).
   - VirtualBox: *Machine → Close → Power off the machine* (not *ACPI
     Shutdown*).
3. Start the VM and log in. After a few seconds a notification appears.
4. Run `beebox last`. Expected verdict:
   - with kernel crash logs working: **The power was cut (or the computer
     was hard-reset)**;
   - without them: **The computer stopped suddenly, with no warning signs**.
     BeeBox says this because, without crash logs, a kernel crash and a
     power cut leave exactly the same evidence, and it won't guess.

## 3. Test running out of memory

1. Stop the memory protector so the system really runs out:

   ```sh
   sudo systemctl stop systemd-oomd 2>/dev/null; sudo systemctl stop earlyoom 2>/dev/null
   sudo apt install stress-ng        # or: sudo dnf install stress-ng
   ```

2. Start eating memory (this makes the VM extremely slow):

   ```sh
   stress-ng --vm 4 --vm-bytes 98% --vm-keep --timeout 5m
   ```

3. Wait until the VM barely responds (30–90 seconds), then cut the power as
   in section 2.
4. Start it again. Expected verdict: **Your computer ran out of memory**,
   naming `stress-ng` as the biggest user.

If you get a different verdict, run `sudo beebox dump > dump.txt` and attach
the file to an issue. It shows the last two minutes the recorder saved, which
tells us whether it kept up during the memory storm.

## 4. Test a kernel crash

A kernel crash stops the computer instantly: nothing can be written to disk
afterwards, not by BeeBox, not by the system journal. The only record that
survives is the kernel's own crash log, saved either in UEFI firmware storage
(pstore) or by kdump, a spare kernel that takes over, saves the log to
`/var/crash` and restarts the computer. So this test needs `beebox doctor` to
show **Kernel crash logs: Saved if the kernel crashes**.

On a **legacy BIOS** VM, set up kdump first: run the command `beebox doctor`
shows on the *Kernel crash logs* line (on Debian, Ubuntu and Kali it installs
`kdump-tools`, switches it on and adds a memory reservation to the boot
menu), restart, then check:

```sh
beebox doctor               # Kernel crash logs: Saved if the kernel crashes (kdump, in /var/crash)
sudo kdump-config show      # Debian family: "current state: ready to kdump"
```

If `kdump-config show` says the reservation is too small, raise the number at
the end of `crashkernel=` in `/etc/default/grub.d/kdump-tools.cfg` (for
example to 256M), run `sudo update-grub` and restart. Without a working crash
log, skip this test: BeeBox will report a sudden stop, which is the honest
answer.

```sh
echo 1 | sudo tee /proc/sys/kernel/sysrq
echo c | sudo tee /proc/sysrq-trigger
```

With kdump, the screen fills with kernel messages for a minute while the
spare kernel saves the log, then the VM restarts by itself: don't power it
off during that. With UEFI crash logs the VM restarts or freezes; if it
freezes, cut the power as in section 2, then start it. Expected verdict:
**A crash was triggered on purpose (SysRq + C)**.

## 5. Check the details

```sh
beebox list
beebox view              # opens the report in the browser
beebox show --json | head
journalctl -u beebox -b  # the recorder's own log
```

If a verdict surprises you, please open an issue and attach
`beebox show --json` (read it first — see [PRIVACY.md](PRIVACY.md)).
