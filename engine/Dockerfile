# Debian-based: @cursor/sdk ships glibc-only native binaries.
FROM node:24-slim
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev || npm install --omit=dev
COPY src ./src
EXPOSE 8090
CMD ["node", "src/server.js"]
