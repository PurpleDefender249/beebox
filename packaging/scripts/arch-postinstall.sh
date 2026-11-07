#!/bin/sh
# Arch Linux convention: packages do not enable services on their own.
cat <<'EOF'

To start recording now and at every boot:

  sudo systemctl enable --now beebox

Then run 'beebox doctor' to check nothing is missing.

EOF
exit 0
