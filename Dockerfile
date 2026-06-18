# Client web UI (hiddify-health serve) with sing-box + xray cores bundled, so
# proxy/subscription testing works inside the container.

# --- build the Go binary ---
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /hiddify-health ./cmd

# --- fetch the cores ---
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS cores
ARG SB_VER=1.13.13
ARG XR_VER=26.3.27
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates unzip && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL -o /tmp/sb.tgz \
      "https://github.com/SagerNet/sing-box/releases/download/v${SB_VER}/sing-box-${SB_VER}-linux-${TARGETARCH}.tar.gz" \
 && tar -xzf /tmp/sb.tgz -C /tmp \
 && mv "/tmp/sing-box-${SB_VER}-linux-${TARGETARCH}/sing-box" /usr/local/bin/sing-box
RUN XR_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "arm64-v8a" || echo "64") \
 && curl -fsSL -o /tmp/xray.zip \
      "https://github.com/XTLS/Xray-core/releases/download/v${XR_VER}/Xray-linux-${XR_ARCH}.zip" \
 && unzip -o /tmp/xray.zip xray -d /usr/local/bin

# --- runtime ---
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build  /hiddify-health        /usr/local/bin/hiddify-health
COPY --from=cores  /usr/local/bin/sing-box /usr/local/bin/sing-box
COPY --from=cores  /usr/local/bin/xray     /usr/local/bin/xray
ENV SINGBOX_BIN=/usr/local/bin/sing-box \
    XRAY_BIN=/usr/local/bin/xray \
    XRAY_CLIENT_PATH=/usr/local/bin/xray \
    XRAY_SERVER_PATH=/usr/local/bin/xray
EXPOSE 8090
ENTRYPOINT ["hiddify-health", "serve", "--addr", ":8090"]
