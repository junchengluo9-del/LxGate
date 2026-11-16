# ========================================================
# Stage: Frontend (Vite)
# ========================================================
FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
COPY internal/web/translation /src/internal/web/translation
RUN npm run build

# ========================================================
# Stage: Builder
# ========================================================
FROM golang:1.27-alpine AS builder
WORKDIR /app
ARG TARGETARCH
ARG TARGETVARIANT

RUN apk --no-cache --update add \
  build-base \
  gcc \
  curl \
  unzip

COPY . .
COPY --from=frontend /src/internal/web/dist ./internal/web/dist

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
# Keep the CGo SQLite object at the architecture's portable baseline: a
# -march=native object from a newer build host SIGILLs on older CPUs.
ARG TARGETARCH
# Self-update / version checks follow this "owner/repo" (docker.yml passes the
# building repository). Empty leaves the panel's self-update disabled with a
# clear message instead of pointing at a placeholder.
ARG LXGATE_RELEASE_REPO=""
RUN case "$TARGETARCH" in \
      amd64) CGO_CFLAGS="$CGO_CFLAGS -O2 -march=x86-64-v2"; CGO_CXXFLAGS="-O2 -march=x86-64-v2" ;; \
    esac; \
    export CGO_CFLAGS CGO_CXXFLAGS; \
    go build -ldflags "-w -s -X github.com/mhsanaei/3x-ui/v3/internal/config.releaseRepo=${LXGATE_RELEASE_REPO}" -o build/lxgate .
RUN ./DockerInit.sh "$TARGETARCH" "$TARGETVARIANT"

# ========================================================
# Stage: Final Image of lxgate
# ========================================================
FROM alpine
ENV TZ=Asia/Tehran
WORKDIR /app

# The VPNGate engine is a Python sidecar: python3 runs it, openvpn owns tun0,
# iproute2/iptables apply the route-nopull policy routing, and kmod provides
# modprobe for the tun device on hosts that load it on demand.
RUN apk add --no-cache --update \
  ca-certificates \
  tzdata \
  fail2ban \
  bash \
  curl \
  openssl \
  python3 \
  openvpn \
  iproute2 \
  iptables \
  kmod

COPY --from=builder /app/build/ /app/
COPY --from=builder /app/DockerEntrypoint.sh /app/
COPY --from=builder /app/lxgate.sh /usr/bin/lxgate
COPY --from=builder /app/internal/web/translation /app/internal/web/translation
# Embedded VPNGate engine (python3 stdlib only) + its state directory.
COPY --from=builder /app/vpngate /app/vpngate
RUN mkdir -p /opt/lxgate/vpngate_data /etc/lxgate


# Configure fail2ban
RUN rm -f /etc/fail2ban/jail.d/alpine-ssh.conf \
  && cp /etc/fail2ban/jail.conf /etc/fail2ban/jail.local \
  && sed -i "s/^\[ssh\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/^\[sshd\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/#allowipv6 = auto/allowipv6 = auto/g" /etc/fail2ban/fail2ban.conf

RUN chmod +x \
  /app/DockerEntrypoint.sh \
  /app/lxgate \
  /usr/bin/lxgate

ENV XUI_IN_DOCKER="true"
# Tells the panel's System & Update page to answer with the docker compose
# command instead of pretending it can self-update inside a container.
ENV LXGATE_DOCKER="1"
ENV XUI_MAIN_FOLDER="/app"
# VPNGate engine bindings/state. The engine API stays on loopback; the SOCKS5
# proxy also stays on loopback (Xray dials it from inside the same container).
ENV LXGATE_VPNGATE_ENABLED="1"
ENV LXGATE_VPNGATE_SCRIPT="/app/vpngate/vpngate_manager.py"
ENV LXGATE_VPNGATE_API_PORT="18787"
ENV LOCAL_PROXY_PORT="7928"
ENV VPNGATE_DATA_DIR="/opt/lxgate/vpngate_data"
ENV XUI_ENABLE_FAIL2BAN="true"
ENV XUI_DB_TYPE=""
ENV XUI_DB_DSN=""
EXPOSE 2053
# /etc/lxgate holds the panel database; /opt/lxgate/vpngate_data holds the
# engine's node cache, tunnel configs and logs. Both must survive a recreate.
VOLUME [ "/etc/lxgate", "/opt/lxgate/vpngate_data" ]
CMD [ "./lxgate" ]
ENTRYPOINT [ "/app/DockerEntrypoint.sh" ]
