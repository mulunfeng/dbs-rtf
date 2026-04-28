#!/bin/bash
# MySQL health check for keepalived
# Exits 0 if MySQL is healthy, 1 if not

MYSQL_HOST=${MYSQL_HOST:-127.0.0.1}
MYSQL_PORT=${MYSQL_PORT:-3306}
MYSQL_USER=${MYSQL_USER:-root}
MYSQL_PASSWORD=${MYSQL_PASSWORD:-rootpass123}

# Try to connect and run a simple query
result=$(mysqladmin -h "$MYSQL_HOST" -P "$MYSQL_PORT" -u "$MYSQL_USER" --password="$MYSQL_PASSWORD" ping 2>/dev/null)

if echo "$result" | grep -q "mysqld is alive"; then
    exit 0
fi

# Fallback: check if port is open
(echo > /dev/tcp/$MYSQL_HOST/$MYSQL_PORT) 2>/dev/null
if [ $? -eq 0 ]; then
    exit 0
fi

exit 1
