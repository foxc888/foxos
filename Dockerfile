ARG MIHOMO_IMAGE=metacubex/mihomo:v1.19.23
FROM ${MIHOMO_IMAGE} AS mihomo-validator

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run typecheck && npm run build

FROM golang:1.25.12-alpine AS server
WORKDIR /src
ARG FOXOS_VERSION=dev
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum* ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN go mod tidy \
    && go mod verify \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${FOXOS_VERSION:-dev}" -o /out/foxos ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates libcap tzdata \
    && addgroup -S -g 10001 foxos \
    && adduser -S -D -H -u 10001 -G foxos foxos \
    && mkdir -p /app/web /data /backups \
    && chown -R foxos:foxos /data /backups
WORKDIR /app
COPY --from=server /out/foxos /app/foxos
COPY --from=web /src/web/dist /app/web
COPY --from=mihomo-validator /mihomo /usr/local/bin/mihomo
RUN setcap cap_net_bind_service=+ep /app/foxos
USER 10001:10001
EXPOSE 80 443
VOLUME ["/data", "/backups"]
ENTRYPOINT ["/app/foxos"]
CMD ["-static", "/app/web", "-database", "/data/foxos.db"]
