#!/bin/sh
# Headless smoke test of index.html, run by `dagger check` (git-atlas:smoke).
# Serves the page, loads it directly and the way gistpreview injects it
# (fetch + document.write), and lets smoke.js drive it.
set -eu
srv=$(mktemp -d)
here=$(cd "$(dirname "$0")" && pwd)
# test.html = index.html + the test driver
sed 's#</body>#<script src="smoke.js"></script></body>#' "$here/index.html" > "$srv/test.html"
cp "$here/smoke.js" "$here/gp.html" "$srv/"
(cd "$srv" && python3 -m http.server 8765 >/dev/null 2>&1) &
for i in 1 2 3 4 5 6 7 8 9 10; do
  python3 -c 'import urllib.request; urllib.request.urlopen("http://127.0.0.1:8765/test.html")' 2>/dev/null && break
  sleep 0.5
done

status=0
for page in test.html gp.html; do
  out=$(chromium-browser --headless --no-sandbox --disable-gpu --disable-dev-shm-usage \
    --virtual-time-budget=15000 --dump-dom "http://127.0.0.1:8765/$page" 2>/dev/null || true)
  result=$(printf '%s' "$out" | grep -o 'id="smoke-result"[^>]*>[^<]*' | sed 's/.*>//' || true)
  echo "$page: ${result:-no result (page did not run)}"
  case "$result" in
    PASS*) ;;
    *) status=1
       printf '%s' "$out" | grep -o 'id="smoke-log"[^>]*>[^<]*' | sed 's/.*>//' | tr '|' '\n' ;;
  esac
done
exit $status
