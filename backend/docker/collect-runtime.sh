#!/bin/bash
set -euo pipefail

install -d /runtime/usr/bin /runtime/etc /runtime/app/data /runtime/usr/share/licenses
install -d -m 1777 /runtime/tmp
install -m 0755 /usr/bin/mysqldump /runtime/usr/bin/mysqldump
# Copy the ELF dependency closure from the same pinned client image.
ldd /usr/bin/mysqldump | awk '{for (i=1; i<=NF; i++) if ($i ~ /^\//) print $i}' | while IFS= read -r lib; do
    cp -L --parents "$lib" /runtime/
done
for path in /etc/pki /etc/crypto-policies /usr/lib64/ossl-modules /usr/share/zoneinfo /usr/share/licenses /usr/share/doc/mysql-community-server-minimal; do
    cp -a --parents "$path" /runtime/
done
printf 'origin:x:10001:10001:Origin:/app:/sbin/nologin\n' > /runtime/etc/passwd
printf 'origin:x:10001:\n' > /runtime/etc/group
printf 'hosts: files dns\n' > /runtime/etc/nsswitch.conf
chown 10001:10001 /runtime/app/data
chmod 0700 /runtime/app/data
test -s /runtime/etc/pki/tls/certs/ca-bundle.crt
test -x /runtime/usr/bin/mysqldump
