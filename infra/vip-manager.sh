#!/bin/bash
# VIP Manager for Docker - manages virtual IP forwarding to MySQL containers
# Usage: ./vip-manager.sh {status|assign|remove|failover}
#
# This script manages a VIP that floats between MySQL containers.
# Run on the Docker host. The VIP is exposed via port mapping.

set -e

VIP="${VIP_ADDRESS:-172.18.0.100}"
VIP_PORT="${VIP_PORT:-3308}"
NETWORK="${NETWORK_NAME:-ha-test}"
LOG_FILE="/tmp/vip-manager.log"

PRIMARY_CONTAINER="${PRIMARY_CONTAINER:-mysql-primary}"
REPLICA_CONTAINER="${REPLICA_CONTAINER:-mysql-replica}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-rootpass123}"
CHECK_INTERVAL="${CHECK_INTERVAL:-5}"

log() {
    echo "$(date -Iseconds) [vip-manager] $*" | tee -a "$LOG_FILE"
}

get_container_ip() {
    docker inspect "$1" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' 2>/dev/null
}

check_mysql() {
    local container="$1"
    local ip
    ip=$(get_container_ip "$container")
    if [ -z "$ip" ]; then
        return 1
    fi
    docker exec "$container" mysqladmin -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" ping &>/dev/null
    return $?
}

current_target() {
    # Find which container has the VIP assigned
    for container in "$PRIMARY_CONTAINER" "$REPLICA_CONTAINER"; do
        if docker exec "$container" ip addr show | grep -q "$VIP" 2>/dev/null; then
            echo "$container"
            return
        fi
    done
    echo "none"
}

assign_vip() {
    local container="$1"
    log "Assigning VIP $VIP to $container"
    docker exec "$container" ip addr add "$VIP"/24 dev eth0 2>/dev/null || true
    log "VIP $VIP now active on $container"
}

remove_vip() {
    local container="$1"
    log "Removing VIP $VIP from $container"
    docker exec "$container" ip addr del "$VIP"/24 dev eth0 2>/dev/null || true
    log "VIP $VIP removed from $container"
}

do_failover() {
    local old_master="$1"
    local new_master="$2"

    log "Failover: $old_master -> $new_master"
    remove_vip "$old_master"
    assign_vip "$new_master"
    log "Failover complete. VIP now on $new_master"
}

auto_heal() {
    log "Starting auto-heal VIP monitoring (interval: ${CHECK_INTERVAL}s)"

    local current_vip_holder
    current_vip_holder=$(current_target)

    if [ "$current_vip_holder" = "none" ]; then
        # No one has the VIP — assign to primary if healthy, else replica
        if check_mysql "$PRIMARY_CONTAINER"; then
            assign_vip "$PRIMARY_CONTAINER"
        elif check_mysql "$REPLICA_CONTAINER"; then
            assign_vip "$REPLICA_CONTAINER"
        else
            log "ERROR: Neither MySQL instance is reachable, cannot assign VIP"
            return 1
        fi
    fi

    while true; do
        current_vip_holder=$(current_target)
        local holder_ip
        holder_ip=$(get_container_ip "$current_vip_holder")

        # Check if the VIP holder's MySQL is still healthy
        if [ "$current_vip_holder" != "none" ]; then
            if ! check_mysql "$current_vip_holder"; then
                log "WARNING: MySQL on $current_vip_holder (VIP holder) is unhealthy!"

                # Try to failover to the other container
                local other
                if [ "$current_vip_holder" = "$PRIMARY_CONTAINER" ]; then
                    other="$REPLICA_CONTAINER"
                else
                    other="$PRIMARY_CONTAINER"
                fi

                if check_mysql "$other"; then
                    do_failover "$current_vip_holder" "$other"
                else
                    log "ERROR: Neither MySQL is reachable, VIP remains on $current_vip_holder"
                fi
            fi
        fi

        sleep "$CHECK_INTERVAL"
    done
}

status() {
    echo "VIP: $VIP"
    echo "Network: $NETWORK"
    echo ""

    for container in "$PRIMARY_CONTAINER" "$REPLICA_CONTAINER"; do
        local ip
        ip=$(get_container_ip "$container")
        local running="no"
        local healthy="no"
        local has_vip="no"

        if docker ps --format '{{.Names}}' | grep -q "^${container}$"; then
            running="yes"
            if check_mysql "$container" 2>/dev/null; then
                healthy="yes"
            fi
            if docker exec "$container" ip addr show 2>/dev/null | grep -q "$VIP"; then
                has_vip="yes"
            fi
        fi

        echo "  $container ($ip):"
        echo "    running: $running"
        echo "    mysql healthy: $healthy"
        echo "    has VIP: $has_vip"
        echo ""
    done

    echo "Current VIP holder: $(current_target)"
}

case "${1:-status}" in
    status)   status ;;
    assign)   assign_vip "$2" ;;
    remove)   remove_vip "$2" ;;
    failover) do_failover "$2" "$3" ;;
    monitor)  auto_heal ;;
    *)
        echo "Usage: $0 {status|assign|remove|failover|monitor}"
        echo "  status              Show VIP status"
        echo "  assign <container>  Assign VIP to container"
        echo "  remove <container>  Remove VIP from container"
        echo "  failover <old> <new> Move VIP from old to new container"
        echo "  monitor             Start auto-heal monitoring"
        ;;
esac
