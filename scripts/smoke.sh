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

# reqms METHOD PATH [json-body] — like req, but also sets HTTP_TIME to
# curl's %{time_total} for the latency assertions (KLOX-47).
reqms() {
	local m="$1" p="$2" d="${3:-}" out
	if [ -n "$d" ]; then
		out="$(curl -sS -o "$RESP" -w '%{http_code} %{time_total}' -b "$JAR" -c "$JAR" -X "$m" \
			-H 'Content-Type: application/json' --connect-timeout 15 --max-time 60 \
			--data-binary "$d" "$BASE$p")"
	else
		out="$(curl -sS -o "$RESP" -w '%{http_code} %{time_total}' -b "$JAR" -c "$JAR" -X "$m" \
			-H 'Content-Type: application/json' --connect-timeout 15 --max-time 60 "$BASE$p")"
	fi
	HTTP_CODE="${out%% *}"; HTTP_TIME="${out##* }"
	printf '%s' "$HTTP_CODE"
}

# assert_fast <label> — fails when the last reqms call took over 1.5 s.
assert_fast() {
	awk "BEGIN{exit !($HTTP_TIME > 1.5)}" \
		&& die "$1 took ${HTTP_TIME}s (> 1.5 s — KLOX-47 latency bound)"
	say "$1 ${HTTP_TIME}s (under 1.5 s)"
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
# The server itself must prove a known posture before smoke touches a
# run, draft, claim or send route — three qualify:
#   - mode=demo: the deterministic demo/replay pipeline; llm auto|off are
#     both model-free (the replay provider answers fixtures).
#   - mode=real + llm_mode=off: the deterministic no-model pipeline over
#     the shared ingested items — catalog sources never fetch per-run and
#     llm.None fails any model call loudly, so the same journey verifies
#     the real app with zero paid AI.
#   - mode=real + llm_mode=auto (KLOX-95): the AI engine is on — the same
#     journey exercises real Anthropic calls inside the KLOX-67 spend
#     guards (≤2 runs/workspace/day, ≤200k tokens/run, ≤$0.25/run,
#     ≤$1/day). A guard refusal lands as run status blocked and dies
#     loudly in wait_run — cap enforcement is part of the evidence.
# An unknown mode or llm_mode fails here — before the first mail- or
# model-capable request. Client flags and NXDOMAIN recipients are not
# evidence.
MODE="$(jget mode)"; LLMMODE="$(jget llm_mode)"
case "$MODE" in
demo)
	case "$LLMMODE" in auto|off) ;; *) die "refusing: unknown llm_mode '$LLMMODE'" ;; esac
	;;
real)
	case "$LLMMODE" in off|auto) ;; *) die "refusing: unknown llm_mode '$LLMMODE'" ;; esac
	;;
*) die "refusing: unknown mode '$MODE' — verification needs the demo pipeline or real mode with llm_mode=off|auto" ;;
esac
# Mail posture decides which send checks run at step 11 (KLOX-59). Live
# mail is not itself a refusal — the production app runs mail=live so
# magic links reach real inboxes. On an unclaimed workspace every send
# route is structurally safe: a readers send hits the claim gate (409
# before the provider is touched) and a test send is the app's own
# dry-run — an outbox ledger write plus a test_sent_at stamp that never
# reaches the provider. The live-mail verification below is exactly
# those two calls, with the ledger row's provider=outbox as proof that
# nothing crossed the wire. A claimed workspace breaks that safety — a
# test send would reach the owner for real — so a live target must also
# report claimed=false. Outbox targets keep the original full journey;
# anything else fails closed.
MAIL="$(jget mail)"; MAILT="$(jget mail_transport)"; CLAIMED="$(jget claimed)"
case "$MAIL" in
outbox)
	[ "$MAILT" = "outbox" ] \
		|| die "refusing: mail=outbox but transport='$MAILT'"
	MAILPOSTURE="outbox"
	;;
live)
	[ "$CLAIMED" = "false" ] \
		|| die "refusing mail-capable checks: live target reports claimed='$CLAIMED' — sends could leave the app"
	MAILPOSTURE="live"
	;;
*) die "refusing mail-capable checks: target reports mail='$MAIL' transport='$MAILT'" ;;
esac
say "transport preflight ok: mode=$MODE llm_mode=$LLMMODE mail=$MAIL transport=$MAILT posture=$MAILPOSTURE"

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

# --- 8b. fill skeleton items -----------------------------------------------
# Under llm off the draft is the honest no-AI skeleton (KLOX-48): items
# carry sources but zero sentences, and empty_items blocks approval.
# Fill each empty item the way the editor does — a claim built from the
# item's own source excerpt (non-consecutive content words, so the
# citation floor verifies it and no copied run can form) through the
# same compose endpoint the "Write this item" box calls — or the
# editor's own take when the item has no usable excerpt. A demo fixture
# draft already has sentences, so the loop exits on the first document.
for i in $(seq 1 40); do
	[ "$(req GET "/api/v1/issues/$IID/document")" = "200" ] || die "document fetch failed"
	REF="$(node -e '
		const fs = require("fs");
		const d = JSON.parse(fs.readFileSync(process.argv[1], "utf8")).document;
		for (const s of d.sections)
			for (const it of s.items) {
				if (it.sentences.length) continue;
				const src = (it.sources || []).find(x => x.excerpt);
				let body = null;
				if (src) {
					const text = (src.excerpt.match(/[\p{L}\p{N}]+/gu) || [])
						.filter(w => w.length >= 4).filter((_, i) => i % 3 === 0)
						.slice(0, 6).join(" ") + ".";
					if (text.length > 1) body = { text, citations: [src.ref] };
				}
				if (!body) body = { text: "Editorial reading of the sources.", kind: "interpretation" };
				body.version = d.version;
				fs.writeFileSync(process.argv[2], JSON.stringify(body));
				console.log(it.ref);
				process.exit(0);
			}
		console.log("");' "$RESP" "$REQBODY")"
	[ -z "$REF" ] && break
	[ "$(reqf POST "/api/v1/issues/$IID/items/$REF/compose" "$REQBODY")" = "200" ] \
		|| die "compose item $REF: $(cat "$RESP")"
done
say "items filled"

# --- 9. resolve every flagged or uncited sentence --------------------------
# Real drafts flag far more sentences than the fixture's handful and can
# leave fact sentences uncited — both block readiness the same way an
# editor would resolve them: remove the sentence. Cap stays a loop guard.
# The first document fetch and the first sentence mutation are timed:
# the KLOX-47 bound fails the smoke above 1.5 s (sentence mutations used
# to take ~9 s on the hosted app — the N+1 document build).
for i in $(seq 1 300); do
	if [ "$i" = "1" ]; then
		# reqms must run outside $(...) — a subshell would discard the
		# HTTP_CODE/HTTP_TIME it sets for assert_fast
		reqms GET "/api/v1/issues/$IID/document" >/dev/null
		[ "$HTTP_CODE" = "200" ] || die "document fetch failed"
		assert_fast "document fetch"
	else
		[ "$(req GET "/api/v1/issues/$IID/document")" = "200" ] || die "document fetch failed"
	fi
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
	if [ "$i" = "1" ]; then
		reqms DELETE "/api/v1/issues/$IID/sentences/$SID" "{\"version\":\"$VER\"}" >/dev/null
		[ "$HTTP_CODE" = "200" ] || die "sentence remove $SID: $(cat "$RESP")"
		assert_fast "sentence remove"
	else
		[ "$(req DELETE "/api/v1/issues/$IID/sentences/$SID" "{\"version\":\"$VER\"}")" = "200" ] \
			|| die "sentence remove $SID: $(cat "$RESP")"
	fi
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
if [ "$MAILPOSTURE" = "live" ]; then
	# Live-mail posture (KLOX-59): the real provider stays configured for
	# magic links, so smoke exercises only the send paths that are safe on
	# an unclaimed workspace. A readers send must hit the claim gate —
	# 409 unclaimed_workspace before the provider is touched — and a test
	# send is the app's dry-run: an outbox ledger write plus a
	# test_sent_at stamp, the issue stays approved, nothing mailed. The
	# claimed-workspace contract (real sends to confirmed readers,
	# delivery outcomes, fallback) is covered locally by the fake-live
	# e2e suite — never against a hosted target.
	code="$(req POST "/api/v1/issues/$IID/send" '{"audience":"readers","when":"now"}')"
	[ "$code" = "409" ] && [ "$(jget error.code)" = "unclaimed_workspace" ] \
		|| die "live target must gate reader sends on claim: got $code $(cat "$RESP")"
	say "claim gate held: readers send refused before the provider (409 unclaimed_workspace)"
	[ "$(req POST "/api/v1/issues/$IID/send" '{"audience":"test","when":"now"}')" = "200" ] \
		|| die "test send: $(cat "$RESP")"
	[ "$(jget delivery)" = "outbox" ] \
		|| die "an unclaimed test send must answer delivery=outbox: $(cat "$RESP")"
	EID="$(jget outbox_email_id)"; [ -n "$EID" ] || die "test send recorded no outbox row"
	[ "$(jget issue.status)" = "approved" ] || die "test send must not mark the issue sent"
	[ -n "$(jget issue.test_sent_at)" ] || die "test send did not stamp test_sent_at"
	[ "$(req GET "/api/v1/outbox/$EID")" = "200" ] || die "outbox email fetch failed"
	[ "$(jget email.provider)" = "outbox" ] \
		|| die "test send reached the provider: $(jget email.provider)"
	say "test send recorded to the outbox ledger (provider=outbox — nothing left the app)"
else
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
		# The publication outbox listing hides account-level magic-link
		# rows (KLOX-66 §8) — the claim response's outbox_email_id is the
		# direct handle to the ledger row carrying the link.
		EID="$(jget outbox_email_id)"
		[ -n "$EID" ] || die "claim response carries no outbox_email_id"
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
