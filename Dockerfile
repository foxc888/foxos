FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS mihomo-build
ARG MIHOMO_VERSION=v1.19.29
ARG MIHOMO_SOURCE_SHA256=1db1cd49c233b67701b596fbd8a963f418ebeca4cb497f38a0e7cd706ea4c630
ARG MIHOMO_X_CRYPTO_VERSION=v0.52.0
ARG MIHOMO_X_NET_VERSION=v0.55.0
ARG MIHOMO_X_OAUTH2_VERSION=v0.27.0
RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /runtime/tmp \
    && chmod 1777 /runtime/tmp
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

FROM scratch AS mihomo-runtime
ARG MIHOMO_VERSION=v1.19.29
LABEL org.opencontainers.image.source="https://github.com/MetaCubeX/mihomo" \
      org.opencontainers.image.title="mihomo" \
      org.opencontainers.image.version="${MIHOMO_VERSION}-foxos1" \
      io.foxos.component="mihomo"
COPY --from=mihomo-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=mihomo-build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=mihomo-build --chmod=1777 /runtime/tmp /tmp
COPY --from=mihomo-build /mihomo /mihomo
VOLUME ["/root/.config/mihomo/"]
ENTRYPOINT ["/mihomo"]

FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS mosdns-build
ARG MOSDNS_VERSION=v0.6.4
ARG MOSDNS_REVISION=2ac30e867a7b40ee0ef70ef85b7dcf7ce56d48d0
ARG MOSDNS_SOURCE_SHA256=a2f63da93ae917e7c5d319cb99cc88a68b437c8eb2e40447a61b935767e3e37c
ARG MOSDNS_X_CRYPTO_VERSION=v0.52.0
ARG MOSDNS_X_NET_VERSION=v0.55.0
RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /runtime/tmp \
    && chmod 1777 /runtime/tmp
WORKDIR /src/mosdns
RUN wget -q -O /tmp/mosdns.tar.gz "https://github.com/jasonxtt/mosdns/archive/${MOSDNS_REVISION}.tar.gz" \
    && echo "${MOSDNS_SOURCE_SHA256}  /tmp/mosdns.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/mosdns.tar.gz --strip-components=1 \
    && rm /tmp/mosdns.tar.gz
RUN GOTOOLCHAIN=local go get \
      "golang.org/x/crypto@${MOSDNS_X_CRYPTO_VERSION}" \
      "golang.org/x/net@${MOSDNS_X_NET_VERSION}" \
    && GOTOOLCHAIN=local go mod tidy \
    && GOTOOLCHAIN=local go mod verify
RUN CGO_ENABLED=0 GOTOOLCHAIN=local go build -mod=readonly -trimpath \
    -ldflags="-s -w -buildid= -X main.version=${MOSDNS_VERSION}-foxos1" \
    -o /mosdns .

FROM scratch AS mosdns-runtime
ARG MOSDNS_VERSION=v0.6.4
ARG MOSDNS_REVISION=2ac30e867a7b40ee0ef70ef85b7dcf7ce56d48d0
LABEL org.opencontainers.image.source="https://github.com/jasonxtt/mosdns" \
      org.opencontainers.image.title="mosdns" \
      org.opencontainers.image.version="${MOSDNS_VERSION}-foxos1" \
      org.opencontainers.image.revision="${MOSDNS_REVISION}" \
      io.foxos.component="mosdns"
ENV MOSDNS_CONTAINER_MODE=1 \
    MOSDNS_CONTAINER_NETWORK_MODE=bridge \
    MOSDNS_AUTO_INIT=0
WORKDIR /cus/mosdns
COPY --from=mosdns-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=mosdns-build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=mosdns-build --chmod=1777 /runtime/tmp /tmp
COPY --from=mosdns-build /mosdns /usr/bin/mosdns
VOLUME ["/cus/mosdns"]
EXPOSE 53/tcp 53/udp 9099/tcp
ENTRYPOINT ["/usr/bin/mosdns", "start", "-d", "/cus/mosdns", "-c", "/cus/mosdns/config_custom.yaml"]

FROM node:22-alpine@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run typecheck && npm run build

FROM golang:1.25.12-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS server
WORKDIR /src
ARG FOXOS_VERSION=dev
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum* ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN go mod tidy \
    && go mod verify \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${FOXOS_VERSION:-dev}" -o /out/foxos ./cmd/server

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce AS foxos-runtime
LABEL org.opencontainers.image.title="foxos" \
      io.foxos.component="foxos"
RUN apk add --no-cache ca-certificates libcap tzdata \
    && addgroup -S -g 10001 foxos \
    && adduser -S -D -H -u 10001 -G foxos foxos \
    && mkdir -p /app/web /data /backups \
    && chown -R foxos:foxos /data /backups
WORKDIR /app
COPY --from=server /out/foxos /app/foxos
COPY --from=web /src/web/dist /app/web
COPY --from=mihomo-build /mihomo /usr/local/bin/mihomo
RUN setcap cap_net_bind_service=+ep /app/foxos
USER 10001:10001
EXPOSE 80 443
VOLUME ["/data", "/backups"]
ENTRYPOINT ["/app/foxos"]
CMD ["-static", "/app/web", "-database", "/data/foxos.db"]
