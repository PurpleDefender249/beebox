#!/bin/sh
# Arch Linux convention: packages do not enable services on their own.
cat <<'EOF'

To start recording now and at every boot:

  sudo systemctl enable --now beebox

Then run 'sudo beebox setup' to switch on what BeeBox needs to catch
crashes, and 'beebox doctor' to check nothing is missing.

EOF
exit 0
