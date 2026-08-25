#!/bin/sh

# Library Genesis search backend.
#
# This exists because Anna's Archive - which upstream searches exclusively - now
# answers /search with an HTTP 403 DDoS-Guard challenge on every known mirror,
# while still returning 200 on its homepage. Libgen serves plain HTML that curl
# can read, and its result rows link to /ads.php?md5=..., which is exactly the
# endpoint lgli_download already fetches. So searching Libgen slots into the
# existing download path with no changes to it.
#
# The catch is that Libgen is unreliable in a way status codes cannot express:
# a mirror may return HTTP 200 with a bare nginx default page, or 200 with a
# results table containing no results. Validation is therefore content-based,
# and requests are retried across mirrors.

# lgli_search <encoded_query> <page> <outfile>
#
# Writes the HTML of a page that genuinely contains results to <outfile>.
# Returns 1 with NET_FAIL_STAGE set if no mirror produced one.
#
# On success LGLI_URL is set to the mirror that worked, so the download that
# follows uses the same host that just answered.
lgli_search() {
    local encoded="$1"
    local page="${2:-1}"
    local out="$3"

    # Try the known-good mirror first when we have one, then everything else.
    local candidates="$LGLI_URL $LGLI_MIRROR_URLS"
    local mirror attempts=0 try last_stage=""

    # Two passes over five mirrors. A "wrong" answer here is usually an
    # immediate 200 rather than a timeout, so the budget costs seconds rather
    # than minutes, and measured success climbs sharply with it.
    local max_attempts=10

    for mirror in $candidates; do
        [ -n "$mirror" ] || continue

        try=0
        while [ "$try" -lt 2 ]; do
            try=$((try + 1))
            attempts=$((attempts + 1))
            # Reassure the user that a retry is in progress, not a hang.
            [ "$attempts" -gt 1 ] && printf '.'

            if http_fetch "$out" "$mirror/index.php?req=${encoded}&page=${page}" --max-time 12; then
                if grep -Fq 'ads.php?md5=' "$out" 2>/dev/null; then
                    [ "$attempts" -gt 1 ] && printf '\n'
                    LGLI_URL="$mirror"
                    NET_FAIL_STAGE=""
                    return 0
                fi
                # HTTP 200 but not a result page. Distinguish the two cases so
                # the user is told something true.
                if grep -Fq 'Welcome to nginx' "$out" 2>/dev/null; then
                    last_stage="nginx-default"
                elif is_junk_response "$out"; then
                    last_stage="challenge"
                else
                    last_stage="empty"
                fi
            else
                last_stage="$NET_FAIL_STAGE"
            fi

            [ "$attempts" -ge "$max_attempts" ] && break
        done

        [ "$attempts" -ge "$max_attempts" ] && break
    done

    [ "$attempts" -gt 1 ] && printf '\n'
    : > "$out" 2>/dev/null
    NET_FAIL_STAGE="${last_stage:-empty}"
    return 1
}

# lgli_parse_results <htmlfile> <base_url>
#
# Emits the same JSON array shape the Anna's Archive parser produces, so
# display_books, the selection loop and lgli_download all keep working
# untouched. The one addition is "source", which makes backend routing explicit
# instead of depending on the substring "lgli" happening to appear somewhere in
# a record.
lgli_parse_results() {
    awk -v base_url="$2" '
        function decode(s) {
            gsub(/&nbsp;/,  " ",  s)
            gsub(/&#39;/,   "\x27", s)
            gsub(/&quot;/,  "\"", s)
            gsub(/&lt;/,    "<",  s)
            gsub(/&gt;/,    ">",  s)
            gsub(/&amp;/,   "\\&", s)
            return s
        }
        # Strip the remainder of the opening <td ...> tag, then all markup.
        function clean(s) {
            sub(/^[^>]*>/, "", s)
            gsub(/<[^>]*>/, " ", s)
            s = decode(s)
            gsub(/[ \t\r\n]+/, " ", s)
            sub(/^ +/, "", s)
            sub(/ +$/, "", s)
            return s
        }
        function json(s) {
            gsub(/\\/, "\\\\", s)
            gsub(/"/,  "\\\"", s)
            return s
        }

        BEGIN {
            RS = "<tr"
            print "["
            count = 0
        }

        {
            # Deliberately not /[a-f0-9]{32}/ - mawk and some busybox builds
            # lack regex interval support, and silently match nothing.
            md5 = ""
            if (match($0, /ads\.php\?md5=[0-9a-f]+/)) {
                md5 = substr($0, RSTART + 12, RLENGTH - 12)
                if (length(md5) > 32) md5 = substr(md5, 1, 32)
                if (length(md5) != 32) md5 = ""
            }
            if (md5 == "") next

            # Cells are located relative to the mirrors cell rather than from
            # the left: the trailing columns are fixed, whereas the leading
            # title cell varies with series/badge markup.
            n = split($0, cell, "<td")
            mcell = 0
            for (k = 2; k <= n; k++) {
                if (index(cell[k], "ads.php?md5=") > 0) { mcell = k; break }
            }
            if (mcell < 3) next

            title = ""; author = ""; publisher = ""; year = ""
            lang = ""; pages = ""; size = ""; ext = ""

            # Title is the edition.php anchor text in the first cell.
            if (match($0, /edition\.php\?id=[0-9]+">[^<]*/)) {
                title = substr($0, RSTART, RLENGTH)
                sub(/^[^>]*>/, "", title)
                title = decode(title)
                gsub(/[ \t\r\n]+/, " ", title)
                sub(/^ +/, "", title); sub(/ +$/, "", title)
            }
            if (title == "") title = clean(cell[2])

            if (mcell - 7 >= 2) author    = clean(cell[mcell - 7])
            if (mcell - 6 >= 2) publisher = clean(cell[mcell - 6])
            if (mcell - 5 >= 2) year      = clean(cell[mcell - 5])
            if (mcell - 4 >= 2) lang      = clean(cell[mcell - 4])
            if (mcell - 3 >= 2) pages     = clean(cell[mcell - 3])
            if (mcell - 2 >= 2) size      = clean(cell[mcell - 2])
            if (mcell - 1 >= 2) ext       = clean(cell[mcell - 1])

            if (title == "") next

            # A short human-readable line for the non-compact listing.
            desc = publisher
            if (year  != "") desc = (desc == "" ? year  : desc " (" year ")")
            if (lang  != "") desc = (desc == "" ? lang  : desc " - " lang)
            if (pages != "" && pages ~ /^[0-9]+$/) desc = desc " - " pages "p"
            if (size  != "") desc = (desc == "" ? size  : desc " - " size)

            if (count > 0) printf ",\n"
            printf "  {\"author\": \"%s\", \"format\": \"%s\", \"md5\": \"%s\", \"title\": \"%s\", \"url\": \"%s/ads.php?md5=%s\", \"description\": \"%s\", \"size\": \"%s\", \"source\": \"lgli\"}", \
                json(author), json(ext), md5, json(title), base_url, md5, json(desc), json(size)
            count++
        }

        END { print "\n]" }
    ' "$1"
}

# Largest page number referenced by the pager, defaulting to 1.
lgli_last_page() {
    local n
    n="$(grep -o -E 'page=[0-9]+' "$1" 2>/dev/null \
         | cut -d= -f2 | sort -nr | head -1)"
    case "$n" in
        ''|*[!0-9]*) echo 1 ;;
        *)           echo "$n" ;;
    esac
}
