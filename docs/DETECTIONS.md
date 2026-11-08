# How BeeBox reaches a verdict

Each rule looks at the evidence on its own and returns a confidence from 0 to
100. The strongest cause at 30 or above becomes the verdict; below that BeeBox
says it couldn't find a clear cause. Confidence is shown in words:

| Score | Shown as |
| --- | --- |
| 80–100 | Very likely |
| 60–79 | Likely |
| 40–59 | Possibly |
| 1–39 | Unclear |

"Near the end" means close to the last sign of life from that boot: the last
recorded sample or journal entry, whichever is later.

| Verdict (ID) | Main signals | Typical confidence |
| --- | --- | --- |
| Out of memory (`out-of-memory`) | Memory pressure "full" in the final minute (≥40% strong, ≥10% moderate), available memory under 3% or 8%, swap over 90% full, the recorder running late, kernel OOM kills or systemd-oomd/earlyoom kills in the last 5 minutes | 30–95 |
| Kernel crash, log not saved (`kernel-crash-unsaved`) | A kdump folder in `/var/crash` from after the boot with no kernel log in it: kdump only starts when the kernel crashes, but it ran out of memory, couldn't read the log or was switched off part-way | 85 |
| Kernel panic (`kernel-panic`) | `Kernel panic - not syncing` in the crashed kernel's own log, from pstore or kdump (95), or the system log (85); the faulting function (`RIP:`/`pc :`) and program (`Comm:`) are named | 85–95 |
| Deliberate crash (`kernel-panic`) | `sysrq: Trigger a crash` near the end | 97 |
| Kernel lockup (`kernel-lockup`) | Soft/hard lockup or RCU stall messages, stronger within 10 minutes of the end | 50–78 |
| Kernel bug (`kernel-bug`) | Oops, NULL pointer dereference, general protection fault | 45–70 |
| Stuck programs (`hung-tasks`) | `blocked for more than N seconds` near the end | 45 |
| Hardware error (`hardware-error`) | Machine-check (MCE) messages | 45–80 |
| Graphics hang (`gpu-hang`) | amdgpu ring timeouts and resets, i915 GPU hangs, nouveau faults, NVIDIA Xid errors; GPU "fallen off the bus" adds weight and power/heat advice | 40–80 |
| Overheating (`overheating`) | Critical-temperature message (90), a sensor within 3 °C of its critical limit (70), CPU at 97 °C or more (55), throttling (30) | 30–90 |
| Battery ran out (`battery-depleted`) | Last reading discharging at ≤3% (90), ≤7% (75), ≤12% (45), or UPower's critical-battery action | 45–90 |
| Never woke from sleep (`died-in-sleep`) | The last power event was a suspend entry with no exit, close to the end; 85 if the battery was at 15% or less | 75–85 |
| Trouble waking (`resume-trouble`) | A device failed to resume shortly before the end | 50 |
| Disk errors (`storage-errors`) | NVMe timeouts, I/O errors, EXT4/Btrfs/XFS errors, ATA failures; stronger within 10 minutes of the end | 35–78 |
| Disk stall (`io-stall`) | I/O pressure "full" ≥40% in the final minute | 45–55 |
| Disk full (`disk-full`) | Less than 1% free on `/` at the end | 45 |
| Froze (`froze`) | The recorder's own check ran ≥5 s late, with no more specific cause | 40 |
| Power cut (`power-loss`) | A recording that stops abruptly while everything was healthy, nothing serious in the logs, on mains power, **on a computer that can save kernel crash logs** with pstore or kdump (so the absence of one rules out a kernel crash) | 55 |
| Sudden stop (`sudden-stop`) | The same without kernel crash logs (45: a power cut and a kernel crash look identical, so BeeBox says so); on battery with charge left (35: hard power-off or hardware lockup); or logs that end with no errors and no recording (35) | 35–45 |
| Unknown (`unknown`) | Nothing above applies | 0 |

Context findings, shown under "Also noticed":

- **SysRq restart** (`sysrq-restart`): you rebooted with Alt + SysRq + REISUB.
- **Low memory earlier** (`low-memory-earlier`): apps were closed for memory
  well before the end.
- **Recorder gap** (`recorder-gap`): the recorder stopped more than two
  minutes before the system's last log entry.
- **Busy processor** (`busy-cpu`): the processor was at least 95% busy for
  the final minute or more, with the app using most of it. A busy CPU makes
  the desktop slow but doesn't stop a computer, so this is context, not a
  cause.

Findings that only repeat the verdict are dropped: for example, "froze" and
"disk stall" under an out-of-memory verdict, or "battery ran out" under "the
battery ran out while asleep".

## Adding a rule

Rules live in `internal/analyze/rules.go`. A rule is a function that receives
the merged timeline and recording metrics and returns a `*report.Finding` or
nil. Add a scenario for it to `internal/scenario` and a case to
`internal/analyze/analyze_test.go` so the verdict is tested end to end.
