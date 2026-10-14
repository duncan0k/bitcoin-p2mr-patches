#!/bin/sh
# End-to-end check of the Linux build against a real Ark-0 node that syncs from node.ark0.si:
#   sh e2e-linux.sh <ark0-witness> <bitcoind> <bitcoin-cli>
# Needs curl and python3, and network access. Uses a fresh data folder under /tmp and removes it at the end.
set -u
WITNESS=$1
BITCOIND=$2
CLI=$3
DATA=$(mktemp -d /tmp/ark0-witness-e2e.XXXXXX)
LOG=$DATA.log
FAILED=0

say() { printf '%s\n' "$*"; }
fail() {
	say "FAILED: $*"
	FAILED=1
}
nodes() { pgrep -f -- "-datadir=$DATA" | wc -l; } # the node only: the witness takes -datadir and the folder as two arguments
start() {
	"$WITNESS" -no-browser -datadir "$DATA" -bitcoind "$BITCOIND" -lang en < /dev/null > "$LOG" 2>&1 &
	PID=$!
	i=0
	while ! URL=$(grep -Eo 'http://127\.0\.0\.1:[0-9]+/[0-9a-f]{64}/' "$LOG" | head -n 1) || [ -z "$URL" ]; do
		i=$((i + 1))
		[ $i -gt 30 ] && { fail "no page address"; cat "$LOG"; return 1; }
		sleep 1
	done
}
field() { python3 -c "import json,sys; s=json.load(sys.stdin); print($1)"; }
wait_running() {
	i=0
	until [ "$(curl -s "${URL}api/status" | field "s['node']")" = running ]; do
		i=$((i + 1))
		[ $i -gt "${1:-120}" ] && { fail "node not running"; return 1; }
		sleep 2
	done
}
stop() {
	kill -TERM "$PID"
	wait "$PID" 2> /dev/null
	i=0
	while [ "$(nodes)" -gt 0 ]; do
		i=$((i + 1))
		[ $i -gt 60 ] && { fail "the node did not stop"; return 1; }
		sleep 1
	done
}

say "== first start and sync"
start || exit 1
wait_running 120
i=0
until curl -s "${URL}api/status" | field "1 if s['node']=='running' and not s['ibd'] and s['blocks']>=s['headers']>15748 else exit(1)" > /dev/null; do
	i=$((i + 1))
	[ $i -gt 300 ] && { fail "no sync"; break; }
	sleep 2
done
STATUS=$(curl -s "${URL}api/status")
say "$STATUS" | field "s['blocks'], s['ark0'], s['h3'], [(w['label'], w['state']) for w in s['tripwires']], s['ecDisabledNextBlock'], s['subversion']"
[ "$(say "$STATUS" | field "s['ark0']")" = ok ] || fail "ark0 check"
[ "$(say "$STATUS" | field "s['h3']")" = 30000 ] || fail "h3"
[ "$(say "$STATUS" | field "sorted(w['label'] for w in s['tripwires'])")" = "['BIP-341 NUMS', 'drill']" ] || fail "tripwire labels"
[ "$(say "$STATUS" | field "s['notM3']")" = False ] || fail "not M3"

say "== boundaries"
PORT=$(say "$URL" | sed -E 's#http://127\.0\.0\.1:([0-9]+)/.*#\1#')
[ "$(curl -s -o /dev/null -w '%{http_code}' -H "Host: evil.example:$PORT" "$URL")" = 421 ] || fail "rebinding host"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/$(printf '%064d' 0)/")" = 404 ] || fail "wrong token"
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: text/plain' -d '{"hex":"00"}' "${URL}api/check")" = 415 ] || fail "content type"
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H 'Origin: https://evil.example' -d '{"hex":"00"}' "${URL}api/check")" = 403 ] || fail "origin"

say "== check a confirmed transaction (block 123's P2MR spend) and a malformed one"
RPCPORT=$(cat "$DATA/witness-rpcport.txt")
CLI="$CLI -datadir=$DATA -signet -rpcport=$RPCPORT"
TX=$($CLI getblock "$($CLI getblockhash 123)" 2 | python3 -c "import json,sys; print(json.load(sys.stdin)['tx'][1]['hex'])")
ANSWER=$(curl -s -X POST -H 'Content-Type: application/json' -d "{\"hex\":\"$TX\"}" "${URL}api/check")
say "$ANSWER"
[ "$(say "$ANSWER" | field "s['allowed']")" = False ] || fail "a confirmed transaction was allowed"
ANSWER=$(curl -s -X POST -H 'Content-Type: application/json' -d '{"hex":"00"}' "${URL}api/check")
say "$ANSWER"
[ -n "$(say "$ANSWER" | field "s['error']")" ] || fail "a malformed transaction was read"
curl -s "${URL}api/report" | grep -q "Checked a transaction" || fail "report"
curl -s "${URL}api/report"

say "== a second window on the same folder shows the first one's page"
"$WITNESS" -no-browser -datadir "$DATA" -bitcoind "$BITCOIND" -lang en < /dev/null > "$LOG.2" 2>&1
grep -q "$URL" "$LOG.2" || fail "second window"
[ "$(nodes)" -eq 1 ] || fail "nodes: $(nodes)"

say "== stop"
stop

say "== restart at once, while the node may still be shutting down"
start && wait_running 120 && stop

say "== a node left running is reused"
start && wait_running 120
kill -KILL "$PID"
sleep 2
[ "$(nodes)" -eq 1 ] || fail "the node did not outlive its witness"
start && wait_running 30
[ "$(nodes)" -eq 1 ] || fail "a second node was started"
stop

pkill -f -- "$DATA" 2> /dev/null
sleep 2
rm -rf "$DATA" "$LOG" "$LOG.2"
[ $FAILED -eq 0 ] && say "E2E OK" || say "E2E FAILED"
exit $FAILED
