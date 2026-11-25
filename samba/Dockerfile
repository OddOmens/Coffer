FROM alpine:3.22
RUN apk add --no-cache samba-server samba-common-tools tini
COPY smb.conf.template share.conf.template /etc/coffer/
COPY entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh
EXPOSE 445
HEALTHCHECK --interval=30s --timeout=5s CMD ["smbstatus", "-b"]
ENTRYPOINT ["/sbin/tini", "--", "/entrypoint.sh"]
