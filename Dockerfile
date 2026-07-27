FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS mihomo-validator
ARG MIHOMO_VERSION=v1.19.29
ARG MIHOMO_SOURCE_SHA256=1db1cd49c233b67701b596fbd8a963f418ebeca4cb497f38a0e7cd706ea4c630
ARG MIHOMO_X_CRYPTO_VERSION=v0.52.0
ARG MIHOMO_X_NET_VERSION=v0.55.0
ARG MIHOMO_X_OAUTH2_VERSION=v0.27.0
RUN apk add --no-cache ca-certificates
WORKDIR /src/mihomo
RUN wget -q -O /tmp/mihomo.tar.gz "https://github.com/MetaCubeX/mihomo/archive/refs/tags/${MIHOMO_VERSION}.tar.gz" \
    && echo "${MIHOMO_SOURCE_SHA256}  /tmp/mihomo.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/mihomo.tar.gz --strip-components=1 \
    && rm /tmp/mihomo.tar.gz
RUN GOTOOLCHAIN=local go get \
      "golang.org/x/crypto@${MIHOMO_X_CRYPTO_VERSION}" \
      "golang.org/x/net@${MIHOMO_X_NET_VERSION}" \
      "golang.org/x/oauth2@${MIHOMO_X_OAUTH2_VERSION}" \
    && GOTOOLCHAIN=local go mod tidy \
    && GOTOOLCHAIN=local go mod verify
RUN CGO_ENABLED=0 GOTOOLCHAIN=local go build -mod=readonly -tags with_gvisor -trimpath \
    -ldflags="-s -w -buildid= -X github.com/metacubex/mihomo/constant.Version=${MIHOMO_VERSION}-foxos1" \
    -o /mihomo .

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
