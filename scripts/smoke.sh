#!/usr/bin/env bash
# smoke.sh <base-url> — end-to-end proof that a deployed Kloxus works:
# health → SPA → sample API → anonymous workspace → onboarding → fixture
# run → draft → flag/commentary fixes → approve → send → reader page.
# Every step fails loudly. Needs bash, curl and node (all already required
# by this repo).
set -u
BASE="${1:-}"
[ -n "$BASE" ] || { echo "usage: scripts/smoke.sh <base-url>" >&2; exit 2; }
BASE="${BASE%/}"

JAR="$(mktemp)"; RESP="$(mktemp)"; REQBODY="$(mktemp)"
trap 'rm -f "$JAR" "$RESP" "$REQBODY"' EXIT

say()  { printf 'smoke: %s\n' "$*"; }
die()  { printf 'SMOKE FAIL: %s\n' "$*" >&2; exit 1; }

# req METHOD PATH [json-body] → prints http code, body lands in $RESP
req() {
	local m="$1" p="$2" d="${3:-}"
	local args=(-sS -o "$RESP" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$m"
		-H 'Content-Type: application/json' --connect-timeout 15 --max-time 60)
	if [ -n "$d" ]; then
		curl "${args[@]}" --data-binary "$d" "$BASE$p"
	else
		curl "${args[@]}" "$BASE$p"
	fi
}

# reqf METHOD PATH <file-with-json-body> — like req, but the body is read
# from a file so UTF-8 payloads survive Windows console codepages.
reqf() {
	local m="$1" p="$2" f="$3"
	curl -sS -o "$RESP" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$m" \
		-H 'Content-Type: application/json' --connect-timeout 15 --max-time 60 \
		--data-binary "@$f" "$BASE$p"
}

# jget <dotted.path> → extracts a JSON value from $RESP ("" when absent)
jget() {
	node -e '
		const p = process.argv[1];
		let s = "";
		process.stdin.on("data", d => s += d).on("end", () => {
			try {
				let v = JSON.parse(s);
				for (const k of p.split(".")) v = v?.[k];
				console.log(v == null ? "" : (typeof v === "object" ? JSON.stringify(v) : v));
			} catch { console.log(""); }
		});' "$1" < "$RESP"
}

# wait_run <run-id> <label> — polls until the run finishes
wait_run() {
	local rid="$1" label="$2" i st
	for i in $(seq 1 150); do
		[ "$(req GET "/api/v1/runs/$rid")" = "200" ] || die "$label: run status request failed"
		st="$(jget run.status)"
		case "$st" in
			done)   say "$label run finished"; return 0 ;;
			failed) die "$label run failed: $(jget run.error)" ;;
			running) sleep 2 ;;
			*) die "$label: unknown run status '$st'" ;;
		esac
	done
	die "$label run did not finish in 5 minutes"
}

# --- 1. health -----------------------------------------------------------
code="$(curl -sS -o "$RESP" -w '%{http_code}' --retry 12 --retry-all-errors \
	--retry-delay 5 --connect-timeout 15 --max-time 90 "$BASE/healthz")"
[ "$code" = "200" ] && grep -q '^ok' "$RESP" || die "healthz -> $code (cold start did not answer)"
say "healthz ok"

# --- 2. landing page serves the SPA --------------------------------------
[ "$(req GET /)" = "200" ] || die "GET / did not return 200"
grep -q 'id="root"' "$RESP" || die "GET / is not the React app"
say "landing serves the SPA"

# --- 3. cited sample (workspace-free) ------------------------------------
[ "$(req GET /api/v1/sample)" = "200" ] || die "GET /api/v1/sample failed"
[ -n "$(jget dossiers)" ] || die "sample payload is empty"
say "GET /api/v1/sample ok"

# --- 4. anonymous workspace ----------------------------------------------
[ "$(req POST /api/v1/demo/new)" = "200" ] || die "workspace creation failed"
WSID="$(jget workspace.id)"; [ -n "$WSID" ] || die "no workspace id returned"
[ "$(req GET /api/v1/workspace)" = "200" ] || die "workspace fetch failed"
say "anonymous workspace $WSID created"

# --- 5. onboard: create publication from the sample proposal -------------
[ "$(req GET /api/v1/proposal)" = "200" ] || die "proposal fetch failed"
node -e '
	const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).proposal;
	const b = { name: d.name, topic: d.topic, audience: d.audience,
		editorial_line: d.editorial_line, tone: d.tone, language: d.language,
		frequency: d.frequency, ai_disclosure: d.ai_disclosure,
		sections: d.sections, paywall: d.paywall };
	require("fs").writeFileSync(process.argv[2], JSON.stringify(b));' "$RESP" "$REQBODY"
[ "$(reqf POST /api/v1/publications "$REQBODY")" = "201" ] || die "publication create: $(cat "$RESP")"
PUB="$(jget publication.id)"; [ -n "$PUB" ] || die "no publication id"
say "publication $PUB created"

# --- 6. confirm the offered fixture sources -------------------------------
[ "$(req GET "/api/v1/publications/$PUB/sources/catalog")" = "200" ] || die "catalog failed"
node -e '
	const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
	require("fs").writeFileSync(process.argv[2], JSON.stringify({ include: d.offered.map(o => o.url) }));' "$RESP" "$REQBODY"
[ "$(reqf POST "/api/v1/publications/$PUB/sources/confirm" "$REQBODY")" = "200" ] || die "source confirm failed"
[ "$(jget sources)" != "[]" ] || die "no sources were added"
say "sources confirmed ($(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).sources.length)' "$RESP"))"

# --- 7. fixture run to completion ----------------------------------------
[ "$(req POST "/api/v1/publications/$PUB/runs")" = "202" ] || die "run start failed"
RID="$(jget run.id)"; [ -n "$RID" ] || die "no run id"
wait_run "$RID" "collection"
[ "$(req GET "/api/v1/runs/$RID")" = "200" ] || die "run refetch failed"
IID="$(jget run.issue_id)"; [ -n "$IID" ] || die "run produced no issue"
say "issue $IID collected"

# --- 8. draft ------------------------------------------------------------
[ "$(req POST "/api/v1/issues/$IID/draft")" = "202" ] || die "draft start failed"
DRID="$(jget run.id)"; [ -n "$DRID" ] || die "no draft run id"
wait_run "$DRID" "draft"

# --- 9. resolve every flagged sentence ------------------------------------
for i in $(seq 1 30); do
	[ "$(req GET "/api/v1/issues/$IID/document")" = "200" ] || die "document fetch failed"
	SIDVER="$(node -e '
		const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).document;
		for (const s of d.sections)
			for (const it of s.items)
				for (const sn of it.sentences)
					if (sn.flag) { console.log(sn.id + "|" + d.version); process.exit(0); }
		console.log("");' "$RESP")"
	[ -z "$SIDVER" ] && break
	SID="${SIDVER%%|*}"; VER="${SIDVER##*|}"
	[ "$(req DELETE "/api/v1/issues/$IID/sentences/$SID" "{\"version\":\"$VER\"}")" = "200" ] \
		|| die "sentence remove $SID: $(cat "$RESP")"
done
say "flags resolved"

# --- 10. required commentary ----------------------------------------------
for i in $(seq 1 10); do
	[ "$(req GET "/api/v1/issues/$IID/document")" = "200" ] || die "document fetch failed"
	rm -f "$REQBODY"
	node -e '
		const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).document;
		for (const s of d.sections)
			if (s.commentary.mode === "required" && !s.commentary.text.trim()) {
				require("fs").writeFileSync(process.argv[2], JSON.stringify({ section: s.title, text: "Smoke check: section read and reviewed.", version: d.version }));
				process.exit(0);
			}' "$RESP" "$REQBODY"
	[ -f "$REQBODY" ] || break
	[ "$(reqf PUT "/api/v1/issues/$IID/commentary" "$REQBODY")" = "200" ] || die "commentary: $(cat "$RESP")"
done
say "commentary saved"

# --- 11. readiness → approve → send ----------------------------------------
[ "$(req GET "/api/v1/issues/$IID/readiness")" = "200" ] || die "readiness failed"
[ "$(jget ready)" = "true" ] || die "issue not ready: $(jget checks)"
[ "$(req POST "/api/v1/issues/$IID/approve")" = "200" ] || die "approve: $(cat "$RESP")"
say "issue approved"
SEND="$(req POST "/api/v1/issues/$IID/send" '{"audience":"readers","when":"now"}')"
LIVEMAIL=""
if [ "$SEND" = "409" ] && [ "$(jget error.code)" = "unclaimed_workspace" ]; then
	# Live delivery: sends need a claimed workspace. The claim link lands
	# in the creator inbox — and in the workspace outbox, where smoke can
	# complete the magic-link round trip headlessly.
	say "workspace unclaimed — claiming by magic link"
	LIVEMAIL=1
	[ "$(req POST /api/v1/workspace/claim '{"email":"smoke@kloxus.dev"}')" = "200" ] \
		|| die "claim: $(cat "$RESP")"
	[ "$(req GET "/api/v1/publications/$PUB/outbox")" = "200" ] || die "outbox list failed"
	EID="$(node -e '
		const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
		const m = d.emails.find(e => e.kind === "magic_link");
		console.log(m ? m.id : "");' "$RESP")"
	[ -n "$EID" ] || die "no magic-link email in the outbox"
	[ "$(req GET "/api/v1/outbox/$EID")" = "200" ] || die "outbox email fetch failed"
	CLAIM="$(node -e '
		const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).email;
		const m = (d.body_md || "").match(/\/claim\?t=[A-Za-z0-9_-]+/);
		console.log(m ? m[0] : "");' "$RESP")"
	[ -n "$CLAIM" ] || die "claim link not found in the outbox email"
	[ "$(req GET "$CLAIM")" = "303" ] || die "claim confirm failed: $(cat "$RESP")"
	say "workspace claimed via magic link"
elif [ "$SEND" != "200" ]; then
	die "send: $(cat "$RESP")"
fi

if [ "$LIVEMAIL" = "1" ]; then
	# Zero confirmed readers is an intended terminal state (D33): nothing
	# leaves the app, the issue stays approved and can go out once a
	# deliverable recipient exists. Assert that contract — delivery:"none"
	# plus a warning — not an unconditional sent.
	[ "$(req POST "/api/v1/issues/$IID/send" '{"audience":"readers","when":"now"}')" = "200" ] \
		|| die "send after claim: $(cat "$RESP")"
	[ "$(jget delivery)" = "none" ] \
		|| die "zero confirmed readers must return delivery=none: $(cat "$RESP")"
	[ "$(jget issue.status)" = "approved" ] \
		|| die "delivery=none must leave the issue approved, got $(jget issue.status)"
	WARN="$(jget warnings)"
	[ -n "$WARN" ] && [ "$WARN" != "[]" ] \
		|| die "delivery=none must carry a warning: $(cat "$RESP")"
	say "zero confirmed readers — delivery:none, issue stays approved"
	# A test send goes to the claimed owner — and confirms them as a
	# reader, so the retry has a deliverable recipient.
	[ "$(req POST "/api/v1/issues/$IID/send" '{"audience":"test","when":"now"}')" = "200" ] \
		|| die "test send: $(cat "$RESP")"
	[ "$(jget to)" = '["smoke@kloxus.dev"]' ] \
		|| die "test send must go to the claimed owner: $(cat "$RESP")"
	case "$(jget delivery)" in resend|outbox_fallback|mixed) ;; *)
		die "unexpected test delivery: $(cat "$RESP")" ;; esac
	[ "$(jget issue.status)" = "approved" ] \
		|| die "a test send must not mark the issue sent"
	[ "$(req POST "/api/v1/issues/$IID/send" '{"audience":"readers","when":"now"}')" = "200" ] \
		|| die "resend after test: $(cat "$RESP")"
	case "$(jget delivery)" in resend|outbox_fallback|mixed) ;; *)
		die "unexpected delivery: $(cat "$RESP")" ;; esac
	[ "$(jget issue.status)" = "sent" ] \
		|| die "issue not marked sent after a deliverable send"
	say "issue sent to the confirmed reader (delivery=$(jget delivery))"
else
	[ "$(jget issue.status)" = "sent" ] || die "issue not marked sent"
	say "issue sent to readers"
fi

# --- 12. reader page --------------------------------------------------------
[ "$(req GET "/api/v1/issues/$IID/after")" = "200" ] || die "after failed"
PURL="$(jget public_url | sed 's/?tok=.*//')"
[ -n "$PURL" ] || die "no public_url"
case "$PURL" in "$BASE"*) ;; *) die "public_url $PURL is not under $BASE" ;; esac
[ "$(req GET "${PURL#$BASE}")" = "200" ] || die "reader page GET failed"
grep -q 'id="root"' "$RESP" || die "reader page is not the SPA"
APIP="$(echo "${PURL#$BASE}" | sed 's#^/p/#/api/v1/p/#')"
[ "$(req GET "$APIP")" = "200" ] || die "reader API $APIP failed"
[ -n "$(jget body_md)" ] || die "reader body is empty"
say "reader page $PURL ok"

say "ALL CHECKS PASSED"
