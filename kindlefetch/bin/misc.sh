#!/bin/sh

# Several mirrors reject curl's default User-Agent outright, so every request
# we make shares this one.
USER_AGENT="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

# Names the stage that failed, so callers can say why a search returned nothing
# instead of printing an empty list. Must stay global - the fetch helpers set it
# as a side channel while their stdout carries the response body.
NET_FAIL_STAGE=""

# open-slum.org publishes live mirror status. Cached so a cold launch costs at
# most one 4.5KB fetch every MIRROR_TTL seconds.
MIRROR_CACHE_FILE="$SCRIPT_DIR/.mirror_cache"
MIRROR_TTL=21600

# Warn at most once per run that we fell back to an unverified TLS connection.
SLUM_INSECURE_WARNED=false

# Cleared once we have positively established that Z-Library is unreachable,
# so the download menu stops offering a backend that cannot work.
ZLIB_AVAILABLE=true

# http_fetch <outfile> <url> [extra curl args...]
#
# Writes the response body to <outfile>. On failure sets NET_FAIL_STAGE to one
# of dns/tls/timeout/network/http-NNN and returns 1.
#
# The body goes to a file rather than stdout so that callers can invoke this
# directly instead of through $(...): a command substitution runs in a subshell,
# and NET_FAIL_STAGE set there would be discarded before the caller could read
# it. Keeping large HTML in a file also avoids holding ~100KB in a shell
# variable on a device with very little RAM.
#
# -sS keeps curl quiet on success but lets its error text through on failure,
# which is what makes the stage classification below possible.
http_fetch() {
    local out="$1" url="$2"
    shift 2

    : > "$out" 2>/dev/null || {
        NET_FAIL_STAGE="no-tmp"
        return 1
    }

    local raw code
    raw="$(curl -sS -L --max-time 15 -A "$USER_AGENT" \
                -o "$out" -w '\n%{http_code}\n' "$@" "$url" 2>&1)"

    # Pick out the status line by shape. curl's stderr and the -w output can
    # interleave, so position is not reliable but "a line that is exactly three
    # digits" is.
    code="$(printf '%s\n' "$raw" | grep -E '^[0-9][0-9][0-9]$' | tail -n1)"

    case "$code" in
        2[0-9][0-9])
            NET_FAIL_STAGE=""
            return 0
            ;;
        ""|000)
            # curl never completed a request; its message is in $raw thanks to -sS.
            case "$raw" in
                *"Could not resolve host"*)  NET_FAIL_STAGE="dns" ;;
                *"certificate"*)             NET_FAIL_STAGE="tls" ;;
                *"SSL"*|*"TLS"*)             NET_FAIL_STAGE="tls" ;;
                *"timed out"*)               NET_FAIL_STAGE="timeout" ;;
                *)                           NET_FAIL_STAGE="network" ;;
            esac
            return 1
            ;;
        *)
            NET_FAIL_STAGE="http-$code"
            return 1
            ;;
    esac
}

# True when a file looks like an anti-bot interstitial rather than real content.
# DDoS-Guard fronts Anna's Archive, DiamWall fronts Z-Library, Cloudflare shows
# "Just a moment". Also catches the bare nginx default page that some Libgen
# backends return with a 200.
is_junk_response() {
    grep -Fq -e 'DDoS-Guard' -e 'ddos-guard' \
             -e 'DiamWall' -e '__diamwall' \
             -e 'Just a moment' -e '_cf_chl' \
             -e 'Checking your browser' \
             -e 'Welcome to nginx' "$1" 2>/dev/null
}

# Percent-encode a string for use in a query value.
#
# Pure awk on purpose: the obvious `od` implementation is not safe here because
# some busybox builds ship without the od applet.
urlencode() {
    awk -v s="$1" '
        BEGIN {
            for (i = 1; i < 256; i++) ord[sprintf("%c", i)] = i
            n = length(s)
            for (i = 1; i <= n; i++) {
                c = substr(s, i, 1)
                if (c ~ /^[A-Za-z0-9._~-]$/)  printf "%s", c
                else if (c == " ")            printf "+"
                else                          printf "%%%02X", ord[c]
            }
        }'
}

change_dns () {
    RESOLV_FILE="/var/run/resolv.conf"
    
    if [ ! -f "$RESOLV_FILE" ]; then
        exit 1
    fi

    sed -i '/^nameserver/d' "$RESOLV_FILE"

    echo "nameserver 1.1.1.1" >> "$RESOLV_FILE"
    echo "nameserver 1.0.0.1" >> "$RESOLV_FILE"
}

load_config() {
    eval "$(base64 -d "$LINK_CONFIG_FILE")"
    if [ -f "$CONFIG_FILE" ]; then
        . "$CONFIG_FILE"
    else
        first_time_setup
    fi
}

load_version() {
    if [ -f "$VERSION_FILE" ]; then
        cat "$VERSION_FILE"
    else
        # Quietly create the file. The old flow printed two lines and slept 4s
        # on every fresh install, which read like an error on the main screen.
        get_version
    fi
}

sanitize_filename() {
    echo "$1" | sed -e 's/[^[:alnum:]\._-]/_/g' -e 's/ /_/g'
}

get_json_value() {
    echo "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | sed "s/\"$2\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\"/\1/" || \
    echo "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*[^,}]*" | sed "s/\"$2\"[[:space:]]*:[[:space:]]*\([^,}]*\)/\1/"
}

ensure_config_dir() {
    local config_dir="$(dirname "$CONFIG_FILE")"
    if [ ! -d "$config_dir" ]; then
        mkdir -p "$config_dir"
    fi
}

cleanup() {
    rm -f "$TMP_DIR"/kindle_books.list \
          "$TMP_DIR"/kindle_folders.list \
          "$TMP_DIR"/search_results.json \
          "$TMP_DIR"/last_search_* \
          "$TMP_DIR"/.kf_probe.* \
          "$TMP_DIR"/.kf_zlogin.* \
          "$TMP_DIR"/.kf_search.*
}

get_version() {
    # Declared separately: `local x="$(cmd)"` reports the exit status of
    # `local`, not of the command substitution, so the `||` below would never
    # fire if these were combined.
    local api_response
    api_response="$(curl -s --max-time 10 -A "$USER_AGENT" \
        -H "Accept: application/vnd.github.v3+json" \
        "https://api.github.com/repos/jokot/KindleFetch/commits")" || {
        # Sent to stderr only, so a Kindle that cannot reach the API does not
        # get three lines of noise on the main screen.
        echo "Failed to fetch version from GitHub API" >&2
        echo "unknown"
        return
    }

    local latest_sha="$(echo "$api_response" | grep -m1 '"sha":' | cut -d'"' -f4 | cut -c1-7)"
    
    echo "$latest_sha" > "$VERSION_FILE"
    load_version
}

check_for_updates() {
    local current_sha="$(load_version)"
    
    local latest_sha="$(curl -s --max-time 10 -A "$USER_AGENT" \
        -H "Accept: application/vnd.github.v3+json" \
        -H "Cache-Control: no-cache" \
        "https://api.github.com/repos/jokot/KindleFetch/commits?per_page=1" | \
        grep -o -E '"sha": "[0-9a-f]+"' | head -1 | cut -d'"' -f4 | cut -c1-7)"
    
    if [ -n "$latest_sha" ] && [ "$current_sha" != "$latest_sha" ]; then
        UPDATE_AVAILABLE=true
        return 0
    else
        return 1
    fi
}

save_config() {
    {
        echo "KINDLE_DOCUMENTS=\"$KINDLE_DOCUMENTS\""
        echo "CREATE_SUBFOLDERS=\"$CREATE_SUBFOLDERS\""
        echo "DEBUG_MODE=\"$DEBUG_MODE\""
        echo "COMPACT_OUTPUT=\"$COMPACT_OUTPUT\""
        echo "ENFORCE_DNS=\"$ENFORCE_DNS\""
        echo "ZLIB_AUTH=\"$ZLIB_AUTH\""
        echo "ZLIB_USERNAME=\"$ZLIB_USERNAME\""
        echo "RESULTS_PER_PAGE=\"$RESULTS_PER_PAGE\""
        echo "ANNAS_URL=\"$ANNAS_URL\""
        echo "LGLI_URL=\"$LGLI_URL\""
        echo "ZLIB_URL=\"$ZLIB_URL\""
    } > "$CONFIG_FILE"
}

# Explain why Z-Library could not be reached, and stop offering it.
#
# Every known mirror currently sits behind DiamWall, which answers curl with a
# 307 redirect to itself and then an HTTP 513 JavaScript challenge. No shell
# client can clear that. Upstream printed a bare "Login failed" here, which
# sent several people hunting for a credential bug that was never there - so
# name the real cause.
zlib_report_block() {
    ZLIB_AVAILABLE=false
    printf '\n' >&2
    case "$1" in
        challenge|http-513|http-307)
            echo "Z-Library is behind a browser challenge (DiamWall) that this tool" >&2
            echo "cannot solve. This is not a problem with your password." >&2
            ;;
        not-json)
            echo "Z-Library returned a web page instead of an API response, meaning" >&2
            echo "the request was intercepted by its bot-protection layer." >&2
            ;;
        dns)     echo "Z-Library: could not resolve the mirror hostname." >&2 ;;
        tls)     echo "Z-Library: TLS handshake failed (device CA bundle may be out of date)." >&2 ;;
        timeout) echo "Z-Library: the mirror timed out." >&2 ;;
        http-*)  echo "Z-Library: mirror returned HTTP ${1#http-}." >&2 ;;
        *)       echo "Z-Library: could not reach the mirror ($1)." >&2 ;;
    esac
    echo "Marking Z-Library unavailable for this session." >&2
}

zlib_login() {
    local zlib_login="$1"
    local zlib_password="$2"

    printf '\nLogging in to Z-Library...'

    local resp="$TMP_DIR/.kf_zlogin.$$"

    if ! http_fetch "$resp" "$ZLIB_URL/eapi/user/login" \
            --max-time 20 \
            -c "$ZLIB_COOKIES_FILE" \
            -H "Content-Type: application/x-www-form-urlencoded" \
            -H "Accept: application/json" \
            -X POST -d "email=$zlib_login&password=$zlib_password"; then
        zlib_report_block "$NET_FAIL_STAGE"
        rm -f "$resp"
        sleep 3
        return 1
    fi

    if is_junk_response "$resp"; then
        zlib_report_block "challenge"
        rm -f "$resp"
        sleep 3
        return 1
    fi

    # A genuine answer is JSON. Anything else means an interstitial was served
    # in place of the API.
    if ! grep -Fq '{' "$resp" 2>/dev/null; then
        zlib_report_block "not-json"
        rm -f "$resp"
        sleep 3
        return 1
    fi

    local response="$(cat "$resp")"
    rm -f "$resp"

    local zlib_username="$(get_json_value "$response" "name" | tr -d '\r\n')"

    if [ -n "$zlib_username" ]; then
        printf "\nSuccessfully logged in as $zlib_username!"
        ZLIB_USERNAME="$zlib_username"
        sleep 2
        return 0
    fi

    # Real JSON but no user object: this one genuinely is a credential problem.
    printf "\nLogin failed: Z-Library rejected those credentials.\n" >&2
    sleep 2
    return 1
}

# Every cookie the login stored, as a single Cookie header value, whatever
# domain it was stored under. In the Netscape jar format the name and value are
# the sixth and seventh fields; HttpOnly entries carry a "#HttpOnly_" prefix on
# the domain, so they must not be skipped as comments.
zlib_auth_cookie() {
    awk -F '\t' '
        /^# / || NF < 7 { next }
        { printf "%s%s=%s", sep, $6, $7; sep = "; " }
    ' "$ZLIB_COOKIES_FILE" 2>/dev/null
}

# Report what this device can actually reach.
#
# Upstream's bug reports were almost all "search returns nothing" with no way to
# tell a blocked mirror from a dead network from a changed page layout. This
# turns the next report into something diagnosable.
run_diagnostics() {
    clear
    echo "Network diagnostics"
    echo "==================="
    echo ""
    curl --version 2>/dev/null | head -1 || echo "curl: not found"
    echo ""

    local t="$TMP_DIR/.kf_diag.$$"
    local target label

    for label in "open-slum|https://open-slum.org/" \
                 "libgen|$LGLI_URL/index.php?req=bible" \
                 "annas|$ANNAS_URL/search?q=bible" \
                 "zlib|$ZLIB_URL/eapi/book/search"; do
        target="${label#*|}"
        case "$target" in
            ''|'/'*) continue ;;
        esac

        # Z-Library is checked through the app API search, which is what the
        # search actually uses; its website is behind DiamWall regardless.
        if [ "${label%%|*}" = "zlib" ]; then
            set -- -X POST -d "message=bible&limit=1" --max-redirs 3
        else
            set --
        fi

        echo "${label%%|*}: $target"
        if http_fetch "$t" "$target" --max-time 10 "$@"; then
            if grep -Fq -e 'ads.php?md5=' -e '"books":[{' "$t" 2>/dev/null; then
                echo "   OK - real results ($(wc -c < "$t") bytes)"
            elif grep -Fq 'status-badge compact' "$t" 2>/dev/null; then
                echo "   OK - status page ($(wc -c < "$t") bytes)"
            elif is_junk_response "$t"; then
                echo "   HTTP 200 but a challenge or placeholder page"
            else
                echo "   HTTP 200, no results ($(wc -c < "$t") bytes)"
            fi
        else
            echo "   FAILED - $NET_FAIL_STAGE"
        fi
        echo ""
    done
    rm -f "$t"

    echo "Libgen candidates: $LGLI_MIRROR_URLS"
    echo ""
    printf "Press Enter to continue..."
    read -r _continue
}

# First entry of a space-separated candidate list. Callers pass the list
# unquoted, so word splitting leaves the first mirror in $1.
first_url() {
    echo "$1"
}

# probe_mirror <base_url> <kind>
#
# Checks the endpoint the app will ACTUALLY use, and validates the response
# body rather than just its status code.
#
# Status codes alone are worthless here. Anna's Archive answers its root with
# 200 while returning 403 on /search, and every Libgen failure mode - a bare
# nginx default page, or a results page containing no results - arrives as a
# perfectly ordinary HTTP 200. The old implementation probed the root and
# accepted anything that was not 000 or 5xx, which is why a mirror could look
# healthy in Settings and still return nothing for every search.
probe_mirror() {
    local base="$1" kind="$2"
    local url marker tmp rc

    case "$kind" in
        lgli)  url="$base/index.php?req=bible" ; marker='ads.php?md5=' ;;
        annas) url="$base/search?q=bible"      ; marker='href="/md5/' ;;
        zlib)  url="$base/eapi/book/search"    ; marker='"books":[{' ;;
        *)     NET_FAIL_STAGE="unknown-kind"   ; return 1 ;;
    esac

    # The Z-Library API only answers search as a POST.
    if [ "$kind" = "zlib" ]; then
        set -- -X POST -d "message=bible&limit=1" --max-redirs 3
    else
        set --
    fi

    tmp="$TMP_DIR/.kf_probe.$$"

    if ! http_fetch "$tmp" "$url" --max-time 8 "$@"; then
        rm -f "$tmp"
        return 1
    fi

    rc=0
    if grep -Fq 'Welcome to nginx' "$tmp" 2>/dev/null; then
        NET_FAIL_STAGE="nginx-default"
        rc=1
    elif is_junk_response "$tmp"; then
        NET_FAIL_STAGE="challenge"
        rc=1
    elif [ -n "$marker" ] && ! grep -Fq "$marker" "$tmp" 2>/dev/null; then
        NET_FAIL_STAGE="empty"
        rc=1
    else
        NET_FAIL_STAGE=""
    fi

    rm -f "$tmp"
    return "$rc"
}

# pick_working_url <kind> <url>...
#
# Echoes the first candidate that serves real content, or returns 1.
#
# Libgen responds correctly only part of the time (measured at roughly one in
# two to one in four requests, the rest being 200-with-no-results), so each
# candidate gets two attempts. The total is capped per kind so that a service
# which is entirely dead cannot stall startup.
pick_working_url() {
    local kind="$1"
    shift

    local max
    case "$kind" in
        annas) max=2 ;;   # every known mirror sits behind DDoS-Guard
        zlib)  max=3 ;;
        *)     max=6 ;;
    esac

    local url attempts=0 try
    for url in "$@"; do
        [ -n "$url" ] || continue
        try=0
        while [ "$try" -lt 2 ]; do
            try=$((try + 1))
            attempts=$((attempts + 1))
            if probe_mirror "$url" "$kind"; then
                echo "$url"
                return 0
            fi
            [ "$attempts" -ge "$max" ] && break
        done
        [ "$attempts" -ge "$max" ] && break
    done
    return 1
}

# Refresh the mirror lists from open-slum.org, which tracks live status for
# these services.
#
# Ordering is the whole point: open-slum distinguishes UP from PROTECTED, and
# PROTECTED means "reachable, but behind DDoS-Guard/Cloudflare" - precisely the
# mirrors that answer curl with a challenge page. So UP candidates come first
# and DOWN/TIMEOUT/UNKNOWN are dropped entirely.
#
# Every failure path leaves the baked-in link_config lists untouched, so a bad
# or missing response can only ever leave us where we started.
refresh_mirror_lists() {
    local html_file="$MIRROR_CACHE_FILE.html"
    local now cached_at age pairs

    now="$(date +%s 2>/dev/null || echo 0)"
    age=999999999
    if [ -f "$MIRROR_CACHE_FILE" ] && [ -s "$html_file" ]; then
        cached_at="$(head -n1 "$MIRROR_CACHE_FILE" 2>/dev/null)"
        case "$cached_at" in
            ''|*[!0-9]*) cached_at=0 ;;
        esac
        [ "$now" -gt 0 ] && age=$((now - cached_at))
    fi

    if [ "$age" -ge "$MIRROR_TTL" ]; then
        if ! http_fetch "$html_file" "https://open-slum.org/" --max-time 10 --compressed; then
            if [ "$NET_FAIL_STAGE" = "tls" ] && \
               http_fetch "$html_file" "https://open-slum.org/" --max-time 10 --compressed -k; then
                # An old Kindle CA bundle predates the Google Trust Services
                # roots Cloudflare now uses. Mirror status is public,
                # non-sensitive data and we only ever read hostnames from it,
                # so continuing unverified is a reasonable trade - but say so.
                if [ "$SLUM_INSECURE_WARNED" != true ]; then
                    echo "Note: open-slum.org certificate could not be verified"
                    echo "      (device CA bundle is likely out of date); continuing unverified."
                    SLUM_INSECURE_WARNED=true
                fi
            else
                return 1
            fi
        fi
        [ "$now" -gt 0 ] && echo "$now" > "$MIRROR_CACHE_FILE" 2>/dev/null
    fi

    [ -s "$html_file" ] || return 1
    grep -Fq 'status-badge compact' "$html_file" 2>/dev/null || return 1

    # One pass in document order. Each domain row is an anchor carrying the URL
    # followed by a status anchor whose href names the service and whose class
    # carries the status:
    #   <a href="https://libgen.bz" class="domain-link" ...>
    #   <a href="libgen.html#libgen.bz" class="status-badge compact up" ...>
    pairs="$(awk '
        /class="domain-link"/ {
            if (match($0, /https:\/\/[^"]+/)) url = substr($0, RSTART, RLENGTH)
            next
        }
        /class="status-badge compact/ {
            svc = ""; st = ""
            if (match($0, /href="[a-z]+\.html#/))  svc = substr($0, RSTART + 6, RLENGTH - 12)
            if (match($0, /compact [a-z]+"/))      st  = substr($0, RSTART + 8, RLENGTH - 9)
            if (url != "" && svc != "" && st != "") print svc, st, url
            url = ""
        }' "$html_file")"

    [ -n "$pairs" ] || return 1

    # Z-Library is left in link_config order. open-slum checks its website,
    # which is PROTECTED on every mirror, and says nothing about the app API
    # that search uses - which works on some of those mirrors and not others.
    local svc list st existing u
    for svc in annas libgen; do
        list=""
        for st in up protected degraded; do
            list="$list $(printf '%s\n' "$pairs" | awk -v s="$svc" -v t="$st" \
                '$1 == s && $2 == t { print $3 }' | tr '\n' ' ')"
        done
        # Collapse whitespace introduced by the per-status passes above.
        list="$(printf '%s' "$list" | tr -s ' ' | sed -e 's/^ *//' -e 's/ *$//')"

        # Require at least two candidates before trusting a parse; anything less
        # suggests the page shape changed and we are better off with link_config.
        [ "$(printf '%s' "$list" | wc -w)" -ge 2 ] || continue

        case "$svc" in
            annas)    existing="$ANNAS_MIRROR_URLS" ;;
            libgen)   existing="$LGLI_MIRROR_URLS" ;;
        esac

        # Append any baked-in mirror open-slum did not rank, rather than
        # dropping it. The status data is a good ordering signal but not the
        # last word: libgen.vg has served real search results while open-slum
        # had it marked DOWN. Reprioritise, never discard.
        for u in $existing; do
            case " $list " in
                *" $u "*) ;;
                *)        list="$list $u" ;;
            esac
        done

        case "$svc" in
            annas)    ANNAS_MIRROR_URLS="$list" ;;
            libgen)   LGLI_MIRROR_URLS="$list" ;;
        esac
    done

    return 0
}