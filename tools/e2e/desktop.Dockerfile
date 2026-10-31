FROM node:24-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends python3 xvfb xauth libgtk-3-0 libnss3 libasound2 libgbm1 libxss1 libatk-bridge2.0-0 libdrm2 libx11-xcb1 libxcomposite1 libxdamage1 libxrandr2 libxfixes3 libxkbcommon0 ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src/desktop
COPY desktop/package*.json ./
RUN npm ci
COPY desktop/ ./
CMD ["sh", "-c", "npm test && xvfb-run -a npm run test:e2e && npm run package -- --linux --x64"]
