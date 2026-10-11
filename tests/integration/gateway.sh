#!/bin/sh
# Start or stop a throwaway OpenShell v0.1.2 gateway with the podman driver
# for make test-gateway (research R-19, upstream container-deployment.mdx).
#
# Unlike the upstream recipe, XDG_STATE_HOME points into the state volume:
# the v0.1.2 image's home (/app) is not writable and the gateway fails to
# create its credential key otherwise. The container runs as its root user,
# which rootless podman maps to the invoking user, so it can open the podman
# socket; label separation is disabled for the socket mount.
#
# The gateway listens on 127.0.0.1:${SANDCAST_TEST_GATEWAY_PORT:-18180}.
# Tests reach it through OPENSHELL_GATEWAY_ENDPOINT, so no gateway is added
# to the user's openshell configuration and the active gateway is unchanged.
#
#   gateway.sh up     start the gateway and wait until it answers
#   gateway.sh down   remove the gateway container and its state volume
set -eu

name=sandcast-test-gateway
volume=sandcast-test-gateway-state
network=sandcast-test
port=${SANDCAST_TEST_GATEWAY_PORT:-18180}
image=${OPENSHELL_GATEWAY_IMAGE:-ghcr.io/nvidia/openshell/gateway:0.1.2}
endpoint=http://127.0.0.1:$port
# The podman driver extracts the sandbox supervisor binary below
# $XDG_DATA_HOME and asks podman to bind-mount it into each sandbox, so the
# directory must exist at the same path on the podman host. A directory in
# the home directory works on Linux and with a podman machine on macOS,
# which shares the home directory at the same path. It also holds the
# sandbox JWT signing keys.
data=${SANDCAST_TEST_GATEWAY_DATA:-$HOME/.cache/sandcast-test-gateway}

case ${1:-up} in
up)
  sock=$(podman info --format '{{.Host.RemoteSocket.Path}}' | sed 's#^unix://##')
  if [ -z "$sock" ]; then
    echo "podman socket not available" >&2
    exit 3
  fi
  podman rm -f "$name" >/dev/null 2>&1 || true
  mkdir -p "$data"
  # Sandbox launches need the gateway's sandbox JWT signing keys. Generate
  # the local PKI with the gateway image and hand the gateway only the jwt/
  # part, so TLS stays disabled.
  if [ ! -f "$data/local/jwt/kid" ]; then
    rm -rf "$data/pki" "$data/local"
    podman run --rm --user 0:0 --security-opt label=disable -v "$data:$data" \
      "$image" generate-certs --output-dir "$data/pki" --server-san 127.0.0.1 >/dev/null
    mkdir -p "$data/local"
    mv "$data/pki/jwt" "$data/local/jwt"
  fi
  # With sandbox JWT keys present the gateway requires user authentication;
  # this throwaway gateway listens on 127.0.0.1 only, so it allows
  # unauthenticated users the way upstream documents for local development.
  # Sandboxes get a dedicated podman network, so another local gateway's
  # "openshell" network is untouched. Sandbox supervisors run in the host
  # network namespace and call the gateway back through its published
  # loopback port; the driver's default assumes the gateway itself runs on
  # the host and would use the container-internal port.
  printf '%s\n' '[openshell]' 'version = 2' '' \
    '[openshell.gateway.auth]' 'allow_unauthenticated_users = true' '' \
    '[openshell.drivers.podman]' "network_name = \"$network\"" "grpc_endpoint = \"http://127.0.0.1:$port\"" \
    > "$data/gateway.toml"
  podman network exists "$network" || podman network create "$network" >/dev/null
  podman run -d --name "$name" \
    --user 0:0 \
    --security-opt label=disable \
    --network "$network" \
    -p "127.0.0.1:$port:8080" \
    -v "$volume:/var/openshell" \
    -v "$sock:/var/run/podman.sock" \
    -e OPENSHELL_COMPUTE_DRIVER=podman \
    -e OPENSHELL_PODMAN_SOCKET=/var/run/podman.sock \
    -e OPENSHELL_DB_URL=sqlite:/var/openshell/openshell.db \
    -e OPENSHELL_DISABLE_TLS=true \
    -e XDG_STATE_HOME=/var/openshell/state \
    -v "$data:$data" \
    -e XDG_DATA_HOME="$data" \
    -e OPENSHELL_LOCAL_TLS_DIR="$data/local" \
    -e OPENSHELL_BIND_ADDRESS=0.0.0.0 \
    -e OPENSHELL_SERVER_PORT=8080 \
    "$image" --config "$data/gateway.toml" >/dev/null
  i=0
  until OPENSHELL_GATEWAY_ENDPOINT=$endpoint openshell status >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -gt 60 ]; then
      echo "gateway did not become ready; logs:" >&2
      podman logs --tail 50 "$name" >&2 || true
      exit 1
    fi
    sleep 1
  done
  echo "$endpoint"
  ;;
down)
  podman rm -f "$name" >/dev/null 2>&1 || true
  podman volume rm -f "$volume" >/dev/null 2>&1 || true
  podman network rm -f "$network" >/dev/null 2>&1 || true
  rm -rf "$data"
  ;;
*)
  echo "usage: $0 up|down" >&2
  exit 2
  ;;
esac
