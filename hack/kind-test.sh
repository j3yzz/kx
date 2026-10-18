#!/usr/bin/env bash
# Runs the integration suite on a throwaway kind cluster (Gate G1).
# Needs docker. Uses `kind` if installed, else `go run` of a pinned version.
# Writes its own kubeconfig; ~/.kube/config is never touched.
#   KEEP=1 hack/kind-test.sh   keep the cluster for debugging
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER=kx-test
KCFG="$PWD/hack/.kx-test.kubeconfig"
if command -v kind >/dev/null; then KIND=(kind); else KIND=(go run sigs.k8s.io/kind@v0.33.0); fi

cleanup() {
  if [ "${KEEP:-}" != 1 ]; then
    "${KIND[@]}" delete cluster --name "$CLUSTER" || true
    rm -f "$KCFG"
  fi
}
trap cleanup EXIT

if "${KIND[@]}" get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  "${KIND[@]}" export kubeconfig --name "$CLUSTER" --kubeconfig "$KCFG"
else
  "${KIND[@]}" create cluster --name "$CLUSTER" --kubeconfig "$KCFG" --wait 120s
fi

kubectl --kubeconfig "$KCFG" apply -f hack/fixtures.yaml
kubectl --kubeconfig "$KCFG" -n kx-test wait --for=condition=available deploy --all --timeout=300s

KX_TEST_KUBECONFIG="$KCFG" go test -tags integration -count=1 -v -timeout 15m ./test/integration/...
