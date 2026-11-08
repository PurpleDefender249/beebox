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
4. Let BeeBox switch on what it needs (the one-line installer already
   offers this). It shows every change and asks first; on a legacy-BIOS VM it
   sets up kdump for kernel crash logs:

   ```sh
   sudo beebox setup
   ```

5. Restart the VM once, log in, and check:

   ```sh
   beebox status
   beebox doctor
   ```

   `beebox status` should say **BeeBox is recording**, and `beebox doctor`
   should end with **Everything BeeBox needs is in place**. If a line still
   shows a command, run `sudo beebox setup` again and restart. The
   **Kernel crash logs** line decides what sections 2 and 4 will report.
6. Wait two minutes before the first test, and run **one test per boot**.
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

1. Stop the memory protector so the system really runs out. `beebox setup`
   switched on systemd-oomd precisely so that this *doesn't* freeze a
   computer, so it has to be stopped for the test (it starts again at the next
   boot):

   ```sh
   sudo systemctl stop systemd-oomd 2>/dev/null; sudo systemctl stop earlyoom 2>/dev/null
   systemctl is-active systemd-oomd earlyoom     # both must say "inactive"
   sudo apt install stress-ng                    # or: sudo dnf install stress-ng
   ```

2. Start eating memory (this makes the VM extremely slow):

   ```sh
   stress-ng --vm 4 --vm-bytes 98% --vm-keep --timeout 5m
   ```

3. Wait until the VM barely responds (30–90 seconds), then cut the power as
   in section 2.
4. Start it again. Expected verdict: **Your computer ran out of memory**,
   naming `stress-ng` as the biggest user.

The VM doesn't always freeze. stress-ng marks its own workers as the kernel's
first choice when memory runs out and starts new ones when they are closed,
and with a fast virtual disk the kernel can keep up, so the VM stays usable:
Linux is protecting itself, which is what you want on a real computer. If the
VM still responds after five minutes, look at `beebox now`: if *memory free*
never got near 0%, memory never ran out (the report will say so too). Cut the
power anyway; the verdict is then **The power was cut**, with what happened to
memory and the processor in the report.

If you get a different verdict, run `sudo beebox dump > dump.txt` and attach
the file to an issue. It shows the last two minutes the recorder saved, which
tells us whether it kept up during the memory storm.

## 4. Test a kernel crash

A kernel crash stops the computer instantly: nothing can be written to disk
afterwards, not by BeeBox, not by the system journal. The only record that
survives is the kernel's own crash log, saved either in UEFI firmware storage
(pstore) or by kdump, a spare kernel that takes over, saves the log to
`/var/crash` and restarts the computer. `sudo beebox setup` (section 1) sets
up whichever this VM can use. Before this test, `beebox doctor` must show
**Kernel crash logs: Saved if the kernel crashes**; if it shows anything
else, run `sudo beebox setup` again and restart.

```sh
echo 1 | sudo tee /proc/sys/kernel/sysrq
echo c | sudo tee /proc/sysrq-trigger
```

With kdump the VM restarts by itself once the spare kernel has saved the log.
That takes from under a minute to a few minutes, and the screen may stay
frozen or black meanwhile (Kali's boot menu hides kernel messages): **wait
five minutes before you cut the power**. With UEFI crash logs the VM
restarts or freezes; if it freezes, cut the power as in section 2, then start
it. Expected verdict:
**A crash was triggered on purpose (SysRq + C)**.

If the verdict is **The kernel crashed (its crash log couldn't be saved)**,
kdump started but didn't finish: usually too little memory for its spare
kernel, or the power was cut too soon. Run `beebox doctor`, then
`sudo beebox setup` if it shows a fix (it gives kdump more memory), restart
and try again. If the verdict is **The power was cut**, kdump didn't start at
all: check `beebox doctor` the same way.

## 5. Check the details

```sh
beebox list
beebox view              # opens the report in the browser
beebox show --json | head
journalctl -u beebox -b  # the recorder's own log
```

If a verdict surprises you, please open an issue and attach
`beebox show --json` (read it first — see [PRIVACY.md](PRIVACY.md)).
