#!/bin/sh
# Prove path-director steers hello.alpha / hello.beta to the right upstream DNS.
set -eu

apk add --no-cache bind-tools >/dev/null

agent=172.28.0.20
alpha=172.28.0.11
beta=172.28.0.12
port=5353

wait_udp() {
	host=$1
	p=$2
	i=0
	while [ "$i" -lt 30 ]; do
		if dig @"$host" -p "$p" +time=1 +tries=1 hello.alpha.test A >/dev/null 2>&1 \
			|| dig @"$host" -p "$p" +time=1 +tries=1 hello.beta.test A >/dev/null 2>&1 \
			|| dig @"$host" -p "$p" +time=1 +tries=1 . SOA >/dev/null 2>&1; then
			return 0
		fi
		# agent answers SERVFAIL for unknown names; any UDP reply means up
		if dig @"$host" -p "$p" +time=1 +tries=1 nosuch.test A 2>/dev/null | grep -q 'status:'; then
			return 0
		fi
		i=$((i + 1))
		sleep 0.5
	done
	echo "timeout waiting for $host:$p" >&2
	return 1
}

echo "== wait for DNS upstreams + agent =="
wait_udp "$alpha" 53
wait_udp "$beta" 53
wait_udp "$agent" "$port"

fail=0
check() {
	label=$1
	got=$2
	want=$3
	if [ "$got" = "$want" ]; then
		echo "OK  $label => $got"
	else
		echo "FAIL $label => got '$got' want '$want'" >&2
		fail=1
	fi
}

echo "== direct upstream (baseline) =="
check "alpha A" "$(dig @"$alpha" +short hello.alpha.test A | head -1)" "10.10.1.1"
check "alpha TXT" "$(dig @"$alpha" +short hello.alpha.test TXT | tr -d '"')" "hello-from-alpha"
check "beta A" "$(dig @"$beta" +short hello.beta.test A | head -1)" "10.10.2.2"
check "beta TXT" "$(dig @"$beta" +short hello.beta.test TXT | tr -d '"')" "hello-from-beta"

echo "== cross-upstream should be empty/NXDOMAIN =="
cross_a="$(dig @"$alpha" +short hello.beta.test A | head -1)"
cross_b="$(dig @"$beta" +short hello.alpha.test A | head -1)"
if [ -z "$cross_a" ] && [ -z "$cross_b" ]; then
	echo "OK  alpha does not answer beta; beta does not answer alpha"
else
	echo "FAIL unexpected cross answers: alpha→beta='$cross_a' beta→alpha='$cross_b'" >&2
	fail=1
fi

echo "== via path-director agent (steering) =="
check "agent→alpha A" "$(dig @"$agent" -p "$port" +short hello.alpha.test A | head -1)" "10.10.1.1"
check "agent→alpha TXT" "$(dig @"$agent" -p "$port" +short hello.alpha.test TXT | tr -d '"')" "hello-from-alpha"
check "agent→beta A" "$(dig @"$agent" -p "$port" +short hello.beta.test A | head -1)" "10.10.2.2"
check "agent→beta TXT" "$(dig @"$agent" -p "$port" +short hello.beta.test TXT | tr -d '"')" "hello-from-beta"

echo "== unmatched name via agent should SERVFAIL =="
status="$(dig @"$agent" -p "$port" +noall +comments nosuch.test A | awk '/status:/{print $6}' | tr -d ',')"
check "agent unmatched status" "$status" "SERVFAIL"

if [ "$fail" -ne 0 ]; then
	echo "DEMO FAILED" >&2
	exit 1
fi
echo "DEMO OK — agent steered alpha/beta to the correct hello-world DNS servers"
