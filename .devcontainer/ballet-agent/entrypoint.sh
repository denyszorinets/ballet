#!/bin/sh
# Starts the Ballet agent in the background when the container is
# configured to reach Core (BALLET_AGENT_CORE_URL), then runs the
# container's command. Pools that run the agent as their command
# (Kubernetes, compose) do not need it.
if [ -n "${BALLET_AGENT_CORE_URL:-}" ] && [ "${1:-}" != "/usr/local/bin/ballet-agent" ]; then
  /usr/local/bin/ballet-agent >>/tmp/ballet-agent.log 2>&1 &
fi
exec "$@"
