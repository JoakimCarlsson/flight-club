FROM oven/bun:1 AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

FROM golang:1.27 AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl fonts-liberation \
    && curl -fsSLo /chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb \
    && apt-get install -y --no-install-recommends /chrome.deb \
    && rm -rf /chrome.deb /var/lib/apt/lists/* \
    && useradd -m -u 10001 app \
    && mkdir -p /data \
    && chown app:app /data
COPY --from=api /out/api /usr/local/bin/api
USER app
WORKDIR /home/app
ENV FLIGHT_CLUB_ADDR=:7080 \
    FLIGHT_CLUB_CHROME_PATH=/usr/bin/google-chrome \
    FLIGHT_CLUB_NO_SANDBOX=true \
    AEROPLAN_SESSION_FILE=/data/aeroplan-session.json \
    UNITED_SESSION_FILE=/data/united-session.json \
    QATAR_SESSION_FILE=/data/qatar-session.json \
    EMIRATES_SESSION_FILE=/data/emirates-session.json \
    BA_SESSION_FILE=/data/britishairways-session.json
VOLUME /data
EXPOSE 7080
ENTRYPOINT ["/usr/local/bin/api"]
