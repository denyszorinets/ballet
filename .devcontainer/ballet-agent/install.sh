#!/bin/sh
# Installs the Ballet agent into a devcontainer image (Feature options
# arrive as POOL, SESSIONUSER, AGENTURL, INSTALLCLAUDE,
# INSTALLOPENCODE). Runs as root at
# image build time.
set -eu

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "ballet-agent: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

# The agent binary: bundled by `make feature`, else downloaded.
here=$(cd "$(dirname "$0")" && pwd)
if [ -f "$here/bin/ballet-agent-$ARCH" ]; then
  install -m 0755 "$here/bin/ballet-agent-$ARCH" /usr/local/bin/ballet-agent
elif [ -n "${AGENTURL:-}" ]; then
  url=$(echo "$AGENTURL" | sed "s/\${ARCH}/$ARCH/g")
  curl -fsSL "$url" -o /usr/local/bin/ballet-agent
  chmod 0755 /usr/local/bin/ballet-agent
else
  echo "ballet-agent: no agent binary: run 'make feature' or set the agentUrl option" >&2
  exit 1
fi

# Claude Code, where every user finds it.
if [ "${INSTALLCLAUDE:-true}" = "true" ] && ! [ -x /usr/local/bin/claude ] && ! [ -x /usr/bin/claude ]; then
  if command -v npm >/dev/null 2>&1; then
    npm install -g @anthropic-ai/claude-code
  else
    HOME=/tmp/ballet-claude-install sh -c 'curl -fsSL https://claude.ai/install.sh | bash'
    install -m 0755 "$(readlink -f /tmp/ballet-claude-install/.local/bin/claude)" /usr/local/bin/claude
    rm -rf /tmp/ballet-claude-install
  fi
fi

# opencode, where every user finds it.
if [ "${INSTALLOPENCODE:-false}" = "true" ] && ! [ -x /usr/local/bin/opencode ] && ! [ -x /usr/bin/opencode ]; then
  if command -v npm >/dev/null 2>&1; then
    npm install -g opencode-ai
  else
    HOME=/tmp/ballet-opencode-install sh -c 'curl -fsSL https://opencode.ai/install | bash'
    install -m 0755 "$(readlink -f /tmp/ballet-opencode-install/.opencode/bin/opencode)" /usr/local/bin/opencode
    rm -rf /tmp/ballet-opencode-install
  fi
fi

# The unprivileged user sessions run as.
user=${SESSIONUSER:-}
if [ -n "$user" ] && ! id "$user" >/dev/null 2>&1; then
  if command -v useradd >/dev/null 2>&1; then
    useradd --create-home --shell /bin/sh "$user"
  else
    adduser -D -s /bin/sh "$user" # Alpine
  fi
fi

# Configuration; BALLET_AGENT_* variables override it at run time.
mkdir -p /etc/ballet
{
  echo "# Written by the ballet-agent devcontainer Feature."
  echo "[agent]"
  if [ -n "${POOL:-}" ]; then echo "labels = [\"pool=$POOL\"]"; fi
  echo "[session]"
  echo "user = \"$user\""
} > /etc/ballet/agent.toml

install -d /usr/local/share/ballet-agent
install -m 0755 "$here/entrypoint.sh" /usr/local/share/ballet-agent/entrypoint.sh
echo "ballet-agent: installed (pool '${POOL:-}', session user '$user')"
