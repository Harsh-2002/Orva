#!/usr/bin/env bash
# onboarding-flow.sh — simulate the browser onboarding flow with curl.
#
# This catches backend regressions in /auth/* and the route-guard semantics
# without needing a real browser. Manual browser pass is documented in
# docs/CAPACITY.md as a checklist.

set -euo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
CONTAINER="${ORVA_CONTAINER:-}"

PASS=0; FAIL=0
check() {
    local label="$1" cond="$2"
    if [ "$cond" = "ok" ]; then echo "ok	$label"; PASS=$((PASS+1))
    else echo "fail	$label	$3"; FAIL=$((FAIL+1)); fi
}

JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

# 1. Pre-state: assume fresh container = no users. Fresh tests start with
# the bootstrap admin key; we test the human-onboarding flow against the
# /auth namespace specifically.
status1=$(curl -sf "$BASE/api/v1/auth/status" | jq -r '.has_user // false')
# The bootstrap admin doesn't create a user (only an API key), so status
# might be false even after an admin key is bootstrapped. We branch on it.
if [ "$status1" = "true" ]; then
    echo "skip	(users already exist; cannot test the onboarding bootstrap)"
    exit 0
fi
check "/api/v1/auth/status returns has_user=false on fresh DB" \
    "$([ "$status1" = "false" ] && echo ok || echo fail)" "got=$status1"

# 2. Onboard.
USER="onboard-$(date +%s)"
PASS_PW="orvatest-pwd-1234"
onboard_resp=$(curl -sfi -c "$JAR" -X POST "$BASE/api/v1/auth/onboard" \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"$USER\",\"password\":\"$PASS_PW\"}")
onboard_code=$(echo "$onboard_resp" | head -1 | awk '{print $2}')
check "/api/v1/auth/onboard → 200" \
    "$([ "$onboard_code" = 200 ] && echo ok || echo fail)" "got=$onboard_code"

# 3. Cookie checks. curl's Netscape jar prefixes HttpOnly cookies with
# `#HttpOnly_<host>`, so we can't filter out leading-`#` lines blindly.
# Match by cookie name instead.
cookie=$(grep -E 'session_token' "$JAR" | tail -1 || true)
check "session_token cookie set" \
    "$([ -n "$cookie" ] && echo ok || echo fail)" "jar empty"

# Netscape jar columns are tab-separated. The expiry is column 5 (an
# epoch timestamp). A 7-day cookie should land 6–8 days in the future.
expires_epoch=$(echo "$cookie" | awk -F'\t' '{print $5}')
now_epoch=$(date +%s)
diff_s=$(( expires_epoch - now_epoch ))
check "cookie ~7d expiry" \
    "$([ "$diff_s" -gt 500000 ] && [ "$diff_s" -lt 700000 ] && echo ok || echo fail)" \
    "diff=${diff_s}s"

# 4. /api/v1/auth/me returns expires_at (post-E.2).
me=$(curl -sf -b "$JAR" "$BASE/api/v1/auth/me")
me_user=$(echo "$me" | jq -r '.username')
me_expires=$(echo "$me" | jq -r '.expires_at')
check "/api/v1/auth/me returns user" \
    "$([ "$me_user" = "$USER" ] && echo ok || echo fail)" "got=$me_user"
check "/api/v1/auth/me returns expires_at" \
    "$([ -n "$me_expires" ] && [ "$me_expires" != null ] && echo ok || echo fail)" \
    "got=$me_expires"

# 5. has_user flips true.
status2=$(curl -sf -b "$JAR" "$BASE/api/v1/auth/status" | jq -r '.has_user')
check "/api/v1/auth/status returns has_user=true after onboard" \
    "$([ "$status2" = "true" ] && echo ok || echo fail)" "got=$status2"

# 6. Session-auth on /api/v1/* works.
fn_list_code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" "$BASE/api/v1/functions")
check "session auth grants /api/v1/* access" \
    "$([ "$fn_list_code" = 200 ] && echo ok || echo fail)" "got=$fn_list_code"

# 7. Idempotency: re-onboard with same user should 409.
re_code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/auth/onboard" \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"$USER\",\"password\":\"$PASS_PW\"}")
check "second /api/v1/auth/onboard → 409" \
    "$([ "$re_code" = 409 ] && echo ok || echo fail)" "got=$re_code"

# 8. /api/v1/auth/refresh issues a fresh cookie + revokes old.
JAR2=$(mktemp)
old_token=$(echo "$cookie" | awk -F'\t' '{print $7}')
refresh_code=$(curl -sf -b "$JAR" -c "$JAR2" -X POST "$BASE/api/v1/auth/refresh" -o /dev/null -w '%{http_code}')
check "/api/v1/auth/refresh → 200" \
    "$([ "$refresh_code" = 200 ] && echo ok || echo fail)" "got=$refresh_code"
new_cookie=$(grep -E 'session_token' "$JAR2" | tail -1 || true)
new_token=$(echo "$new_cookie" | awk -F'\t' '{print $7}')
check "refresh issued a different token" \
    "$([ -n "$new_token" ] && [ "$new_token" != "$old_token" ] && echo ok || echo fail)" \
    "old=$old_token new=$new_token"

# Old token should no longer work for /api/v1/auth/me.
old_jar=$(mktemp)
echo "# Netscape HTTP Cookie File" > "$old_jar"
# Construct a jar using the old cookie. Simpler: send raw header.
old_me_code=$(curl -s -o /dev/null -w '%{http_code}' -H "Cookie: session_token=$old_token" "$BASE/api/v1/auth/me")
check "old token revoked after refresh" \
    "$([ "$old_me_code" = 401 ] && echo ok || echo fail)" "got=$old_me_code"
rm -f "$old_jar"

# 9. Logout invalidates the new session.
curl -sf -b "$JAR2" -X POST "$BASE/api/v1/auth/logout" > /dev/null
post_logout_code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR2" "$BASE/api/v1/auth/me")
check "logout invalidates session" \
    "$([ "$post_logout_code" = 401 ] && echo ok || echo fail)" "got=$post_logout_code"

# 10. DB sanity (if container is reachable AND has sqlite3 available).
# The slim image doesn't ship sqlite3 by design — skip silently rather
# than failing the whole suite.
if [ -n "$CONTAINER" ] && docker exec "$CONTAINER" sh -c 'command -v sqlite3 >/dev/null 2>&1'; then
    user_count=$(docker exec "$CONTAINER" sh -c "sqlite3 /var/lib/orva/orva.db 'SELECT COUNT(*) FROM users WHERE username=\"$USER\"'")
    check "user persisted in SQLite" \
        "$([ "$user_count" = 1 ] && echo ok || echo fail)" "count=$user_count"
fi

rm -f "$JAR2"
echo
echo "onboarding-flow	pass=$PASS	fail=$FAIL"
exit $FAIL
