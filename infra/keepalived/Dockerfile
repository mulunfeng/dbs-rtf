FROM alpine:3.19

RUN apk add --no-cache keepalived iproute2 bash iputils

COPY check_mysql.sh /usr/local/bin/check_mysql.sh
RUN chmod +x /usr/local/bin/check_mysql.sh

COPY keepalived.conf /etc/keepalived/keepalived.conf

CMD ["keepalived", "-n", "-l", "-f", "/etc/keepalived/keepalived.conf", "--log-console"]
