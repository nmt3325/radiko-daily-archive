#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${RADIKO_PROXY_URL:-}" ]]; then
  echo "RADIKO_PROXY_URL is not configured. The runner must already have Japanese egress."
  exit 0
fi

if [[ -z "${GITHUB_ENV:-}" ]]; then
  echo "GITHUB_ENV is not available" >&2
  exit 1
fi

echo "::add-mask::${RADIKO_PROXY_URL}"
for name in HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy; do
  printf '%s=%s\n' "$name" "$RADIKO_PROXY_URL" >> "$GITHUB_ENV"
done

echo "Configured the private proxy for subsequent steps."
