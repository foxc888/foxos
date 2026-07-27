#!/bin/sh

# Transparent ingress is deliberately disabled in the trusted baseline. Do not
# create routes or wait for tun0 here: RouterOS Container has not yet passed an
# end-to-end TUN, return-path, management-bypass, FastTrack, and exit-IP test.
echo "Starting mihomo..."
exec /mihomo -d /root/.config/mihomo
