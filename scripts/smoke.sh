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
	# The real app fetches live feeds and calls the model — a run takes
	# minutes, not the fixture's seconds. 400×2 s ≈ 13 min, inside the
	# server's own 15-minute run bound.
	local rid="$1" label="$2" i st
	for i in $(seq 1 400); do
		[ "$(req GET "/api/v1/runs/$rid")" = "200" ] || die "$label: run status request failed"
		st="$(jget run.status)"
		case "$st" in
			done)   say "$label run finished"; return 0 ;;
			failed) die "$label run failed: $(jget run.error)" ;;
			blocked) die "$label run blocked by spend guard ($(jget run.blocked_reason)): $(jget run.error)" ;;
			running) sleep 2 ;;
			*) die "$label: unknown run status '$st'" ;;
		esac
	done
	die "$label run did not finish in 13 minutes"
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

# --- 4b. transport preflight — fail closed before mail/model-capable ops ---
# Hosted verification is outbox-only under the freeze. The server itself
# must prove a no-AI posture before smoke touches a run, draft, claim or
# send route — two qualify:
#   - mode=demo: the deterministic demo/replay pipeline; llm auto|off are
#     both model-free (the replay provider answers fixtures).
#   - mode=real + llm_mode=off: the deterministic no-model pipeline over
#     the shared ingested items — catalog sources never fetch per-run and
#     llm.None fails any model call loudly, so the same journey verifies
#     the real app with zero paid AI.
# A real target with the model enabled, an unknown mode, or any live or
# unknown mail transport fails here — before the first mail- or
# model-capable request. Client flags and NXDOMAIN recipients are not
# evidence.
MODE="$(jget mode)"; LLMMODE="$(jget llm_mode)"
case "$MODE" in
demo)
	case "$LLMMODE" in auto|off) ;; *) die "refusing: unknown llm_mode '$LLMMODE'" ;; esac
	;;
real)
	[ "$LLMMODE" = "off" ] \
		|| die "refusing: real target reports llm_mode='$LLMMODE' — verification needs the deterministic pipeline (llm_mode=off)"
	;;
*) die "refusing: unknown mode '$MODE' — verification needs the demo pipeline or real mode with llm_mode=off" ;;
esac
[ "$(jget mail)" = "outbox" ] && [ "$(jget mail_transport)" = "outbox" ] \
	|| die "refusing mail-capable checks: target reports mail='$(jget mail)' transport='$(jget mail_transport)' — hosted verification is outbox-only"
say "transport preflight ok: mode=$MODE llm_mode=$LLMMODE, outbox mail"

# --- 4c. the operator admin surface stays guarded --------------------------
# Admin routes bypass workspace resolution and carry operator power (the
# LLM kill switch, the ingestion fetch log). Hosted smoke must prove they
# answer only to credentials: without one, /admin/llm replies 401 (token
# configured) or 403 (surface disabled), and the master-key ingestion
# view replies 403 — or 404 when no key is configured and the route
# looks absent. A 200 means the guard is off and the posture is unsafe.
code="$(req GET /api/v1/admin/llm)"
case "$code" in 401|403) ;; *) die "unguarded admin surface: GET /api/v1/admin/llm -> $code" ;; esac
code="$(req GET /api/v1/admin/ingestion)"
case "$code" in 403|404) ;; *) die "unguarded admin surface: GET /api/v1/admin/ingestion -> $code" ;; esac
say "admin surface guarded"

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

# --- 9. resolve every flagged or uncited sentence --------------------------
# Real drafts flag far more sentences than the fixture's handful and can
# leave fact sentences uncited — both block readiness the same way an
# editor would resolve them: remove the sentence. Cap stays a loop guard.
for i in $(seq 1 300); do
	[ "$(req GET "/api/v1/issues/$IID/document")" = "200" ] || die "document fetch failed"
	SIDVER="$(node -e '
		const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).document;
		for (const s of d.sections)
			for (const it of s.items)
				for (const sn of it.sentences)
					if (sn.flag || (sn.kind === "fact" && !(sn.citations && sn.citations.length))) {
						console.log(sn.id + "|" + d.version); process.exit(0);
					}
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
# The preflight proved mail_transport=outbox: the send records to the
# outbox ledger and nothing leaves the app. The live-mail contract
# (claim gate, confirmed readers, delivery:"none") is covered locally by
# the fake-live e2e suite — never against a hosted target.
SEND="$(req POST "/api/v1/issues/$IID/send" '{"audience":"readers","when":"now"}')"
if [ "$SEND" = "409" ] && [ "$(jget error.code)" = "unclaimed_workspace" ]; then
	# Live delivery: sends need a claimed workspace. The claim link lands
	# in the creator inbox — and in the workspace outbox, where smoke can
	# complete the magic-link round trip headlessly.
	say "workspace unclaimed — claiming by magic link"
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
	# Two-step claim: the emailed link lands a confirm page (a scanner's
	# prefetch must not spend the token); its explicit POST claims.
	[ "$(req GET "$CLAIM")" = "200" ] || die "claim page failed: $(cat "$RESP")"
	grep -q 'Confirm claim' "$RESP" || die "claim page is not the confirm form"
	[ "$(req POST "$CLAIM")" = "303" ] || die "claim confirm failed: $(cat "$RESP")"
	say "workspace claimed via magic link"
	[ "$(req POST "/api/v1/issues/$IID/send" '{"audience":"readers","when":"now"}')" = "200" ] \
		|| die "send after claim: $(cat "$RESP")"
elif [ "$SEND" != "200" ]; then
	die "send: $(cat "$RESP")"
else
	[ "$(jget delivery)" = "outbox" ] \
		|| die "an outbox transport must answer delivery=outbox: $(cat "$RESP")"
fi
[ "$(jget issue.status)" = "sent" ] || die "issue not marked sent"
say "issue delivered to the outbox"

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
