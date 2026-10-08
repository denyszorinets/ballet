#!/bin/sh
# A stand-in for the claude CLI in failure-injection tests: it calls the
# LLM gateway, works for FAKE_AGENT_SLEEP seconds (from the project's
# execution env), calls the gateway again and reports a result in claude's
# stream-json format; then it waits for standard input to close, as claude
# does between turns. A failed gateway call fails the session.
call() {
  curl -sf -X POST "$ANTHROPIC_BASE_URL/v1/messages" -H "x-api-key: $ANTHROPIC_API_KEY" \
    -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \
    -d '{"model":"fake","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}' >/dev/null
}
read -r _ # the prompt, one stream-json message
if ! call; then
  echo '{"type":"result","is_error":true,"result":"the gateway did not answer at the start","num_turns":1}'
  exit 1
fi
sleep "${FAKE_AGENT_SLEEP:-4}"
if ! call; then
  echo '{"type":"result","is_error":true,"result":"the gateway did not answer at the end","num_turns":1}'
  exit 1
fi
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"fake work done","num_turns":1,"total_cost_usd":0}'
cat >/dev/null
