#!/bin/bash
# ═══════════════════════════════════════════════════════
#  OS-level kernel tuning for 10 Gbps throughput
#
#  Run on the HOST machine (not inside Docker):
#    sudo bash scripts/sysctl-10g.sh
#
#  Хэрэгтэй: server дээр root эрхтэй ажиллуулна
# ═══════════════════════════════════════════════════════

set -e

echo "══════════════════════════════════════"
echo "  Applying 10 Gbps kernel tuning"
echo "══════════════════════════════════════"

# ── Network buffers ──
sysctl -w net.core.rmem_max=16777216
sysctl -w net.core.wmem_max=16777216
sysctl -w net.core.rmem_default=1048576
sysctl -w net.core.wmem_default=1048576
sysctl -w net.ipv4.tcp_rmem="4096 1048576 16777216"
sysctl -w net.ipv4.tcp_wmem="4096 1048576 16777216"

# ── Connection backlog ──
sysctl -w net.core.somaxconn=65535
sysctl -w net.ipv4.tcp_max_syn_backlog=65535
sysctl -w net.core.netdev_max_backlog=65535

# ── TCP tuning ──
sysctl -w net.ipv4.tcp_fastopen=3
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_fin_timeout=15
sysctl -w net.ipv4.tcp_keepalive_time=300
sysctl -w net.ipv4.tcp_keepalive_intvl=15
sysctl -w net.ipv4.tcp_keepalive_probes=5
sysctl -w net.ipv4.tcp_max_tw_buckets=2000000
sysctl -w net.ipv4.tcp_slow_start_after_idle=0

# ── Local port range (more ephemeral ports) ──
sysctl -w net.ipv4.ip_local_port_range="1024 65535"

# ── File descriptors ──
sysctl -w fs.file-max=2097152
sysctl -w fs.nr_open=2097152

# ── Persistent (survives reboot) ──
cat > /etc/sysctl.d/99-10g-cache.conf << 'EOF'
# 10 Gbps Cache Server tuning
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.core.rmem_default=1048576
net.core.wmem_default=1048576
net.ipv4.tcp_rmem=4096 1048576 16777216
net.ipv4.tcp_wmem=4096 1048576 16777216
net.core.somaxconn=65535
net.ipv4.tcp_max_syn_backlog=65535
net.core.netdev_max_backlog=65535
net.ipv4.tcp_fastopen=3
net.ipv4.tcp_tw_reuse=1
net.ipv4.tcp_fin_timeout=15
net.ipv4.tcp_keepalive_time=300
net.ipv4.tcp_keepalive_intvl=15
net.ipv4.tcp_keepalive_probes=5
net.ipv4.tcp_max_tw_buckets=2000000
net.ipv4.tcp_slow_start_after_idle=0
net.ipv4.ip_local_port_range=1024 65535
fs.file-max=2097152
fs.nr_open=2097152
EOF

# ── File descriptor limits ──
cat > /etc/security/limits.d/99-10g-cache.conf << 'EOF'
* soft nofile 1048576
* hard nofile 1048576
root soft nofile 1048576
root hard nofile 1048576
EOF

echo ""
echo "Done! Verify:"
echo "  sysctl net.core.rmem_max"
echo "  sysctl net.core.somaxconn"
echo "  ulimit -n"
