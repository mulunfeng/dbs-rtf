notify_master() {
    echo "$(date -Iseconds) keepalived transitioned to MASTER (VIP active on this node)"
    echo "$(date -Iseconds) keepalived transitioned to MASTER" >> /var/log/keepalived-events.log
}

notify_backup() {
    echo "$(date -Iseconds) keepalived transitioned to BACKUP (VIP moved away)"
    echo "$(date -Iseconds) keepalived transitioned to BACKUP" >> /var/log/keepalived-events.log
}

notify_fault() {
    echo "$(date -Iseconds) keepalived entered FAULT state"
    echo "$(date -Iseconds) keepalived entered FAULT" >> /var/log/keepalived-events.log
}

ROLE="${1:-unknown}"
case "$ROLE" in
    master)  notify_master ;;
    backup)  notify_backup ;;
    fault)   notify_fault ;;
    *)       echo "Unknown role: $ROLE" ;;
esac
