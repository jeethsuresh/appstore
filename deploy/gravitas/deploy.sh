#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
: "${1:?Pass the immutable ghcr.io image digest}"
client="${KUBECTL:-kubectl}"
cluster=("$client" --context "${KUBE_CONTEXT:-do-tor1-gravitas-k8s-services}")
server="$("${cluster[@]}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
[[ "$server" == 'https://02fe9a55-8048-421e-a03f-eae9a4a60472.k8s.ondigitalocean.com' ]] || { echo 'Refusing an unexpected cluster' >&2; exit 1; }
python3 render.py "$1" | "${cluster[@]}" apply --server-side --field-manager=appstore-deploy -f -
"${cluster[@]}" -n appstore rollout status deployment/appstore --timeout=300s
actual="$("${cluster[@]}" -n appstore get deployment/appstore -o jsonpath='{.spec.template.spec.containers[0].image}')"
[[ "$actual" == "$1" ]] || { echo "Deployment image differs from the tested release" >&2; exit 1; }
curl --fail --silent --show-error --retry 5 --retry-delay 5 https://apps.gravitas.cool/healthz
status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' https://apps.gravitas.cool/v1/apps)"
[[ "$status" == '401' ]] || { echo "Unauthenticated catalog returned $status, expected 401" >&2; exit 1; }
