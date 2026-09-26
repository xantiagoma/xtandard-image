#!/bin/sh
# atmoz/sftp runs /etc/sftp.d/* before sshd: install the committed test host
# key (mounted read-only at /keys) with the 0600 mode sshd insists on, so the
# integration tests can pin it (sftpsrc refuses unpinned host keys).
set -eu
install -m 600 /keys/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
install -m 644 /keys/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ed25519_key.pub
