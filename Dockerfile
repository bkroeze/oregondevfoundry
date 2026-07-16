FROM node:24-alpine

ENV PORT=8080

WORKDIR /app
COPY --chown=node:node --chmod=644 index.html styles.css script.js server.js ./
USER node

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=3s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1

CMD ["node", "server.js"]
