FROM nginxinc/nginx-unprivileged:1.29-alpine

ENV PORT=8080

USER root
RUN mkdir -p /etc/nginx/templates \
    && chown 101:101 /etc/nginx/templates \
    && chmod 755 /etc/nginx/templates
COPY --chown=101:101 --chmod=644 nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --chown=101:101 --chmod=644 index.html styles.css script.js /usr/share/nginx/html/
USER 101

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=3s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/" || exit 1
