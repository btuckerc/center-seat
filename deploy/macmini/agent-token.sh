#!/bin/sh
# Manage CENTERSEAT_AGENT_TOKEN in the production .env without displaying it.
#
#   agent-token.sh status   show whether a token is set, plus a short fingerprint
#   agent-token.sh rotate   write a new random token, recreate the web container
#   agent-token.sh print    write the raw token to stdout; refuses a terminal
#
# Copy to the clipboard on your Mac without the value appearing on screen:
#   ssh mini 'cd ~/src/centerseat && deploy/macmini/agent-token.sh print' | pbcopy
set -eu

cd "$(dirname "$0")/../.."
env_file=.env
key=CENTERSEAT_AGENT_TOKEN

current() {
  [ -f "$env_file" ] || return 0
  sed -n "s/^${key}=//p" "$env_file" | tail -n 1
}

fingerprint() {
  printf '%s' "$1" | shasum -a 256 | cut -c1-12
}

case "${1:-}" in
  status)
    token=$(current)
    if [ -n "$token" ]; then
      echo "agent token set (sha256 prefix $(fingerprint "$token"))"
    else
      echo "agent token not set; /api/agent/* and /api/mcp return 404"
    fi
    ;;
  rotate)
    umask 077
    token=$(openssl rand -hex 32)
    tmp=$(mktemp "${env_file}.XXXXXX")
    if [ -f "$env_file" ]; then grep -v "^${key}=" "$env_file" >"$tmp" || true; fi
    printf '%s=%s\n' "$key" "$token" >>"$tmp"
    mv "$tmp" "$env_file"
    PATH="/usr/local/bin:/opt/homebrew/bin:$PATH" docker compose -p centerseat \
      -f compose.yaml -f deploy/macmini/compose.override.yaml up -d web >/dev/null
    echo "agent token rotated (sha256 prefix $(fingerprint "$token")); update every agent that uses it"
    ;;
  print)
    if [ -t 1 ]; then
      echo "refusing to print the token to a terminal; pipe it, e.g. | pbcopy" >&2
      exit 1
    fi
    token=$(current)
    [ -n "$token" ] || { echo "agent token not set" >&2; exit 1; }
    printf '%s' "$token"
    ;;
  *)
    echo "usage: $0 status|rotate|print" >&2
    exit 2
    ;;
esac
