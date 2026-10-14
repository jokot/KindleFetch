#!/bin/sh

# Z-Library search backend.
#
# The Z-Library website sits behind DiamWall, which answers curl with a 307
# loop. Its JSON API - the one the official mobile app uses, under /eapi/ - is
# not behind that challenge on every mirror, and it answers search without an
# account. Each result carries the id and hash that /eapi/book/{id}/{hash}/file
# needs, so a download never has to touch the blocked website either.

# Results fetched per search. display_books pages through these locally, since
# the selection loop never goes back to the network for another page.
ZLIB_SEARCH_LIMIT=50

# Map the filter menu's language codes to the names the API expects. A custom
# entry that is already a name ("indonesian") passes through unchanged.
zlib_language_name() {
    case "$1" in
        '')  ;;
        en)  echo english ;;
        ru)  echo russian ;;
        es)  echo spanish ;;
        de)  echo german ;;
        fr)  echo french ;;
        it)  echo italian ;;
        pt)  echo portuguese ;;
        nl)  echo dutch ;;
        pl)  echo polish ;;
        tr)  echo turkish ;;
        id)  echo indonesian ;;
        zh)  echo chinese ;;
        ja)  echo japanese ;;
        ar)  echo arabic ;;
        ??)  ;;
        *)   echo "$1" | tr 'A-Z' 'a-z' ;;
    esac
}

# zlib_search <query> <outfile>
#
# Writes the JSON of a response that contains at least one book to <outfile>.
# Returns 1 with NET_FAIL_STAGE set if no mirror produced one: "no-match" when
# Z-Library answered and genuinely found nothing, otherwise why it failed.
#
# On success ZLIB_URL is set to the mirror that answered, so the download that
# follows goes to the same host.
zlib_search() {
    local query="$1" out="$2"

    # Declared local so that sourcing the filter file below cannot leak them.
    local content_filter="" ext_filter="" lang_filter="" src_filter="" sort_filter=""
    [ -f "$SCRIPT_DIR/tmp/current_filters" ] && . "$SCRIPT_DIR/tmp/current_filters" 2>/dev/null

    # The query goes through --data-urlencode, so it needs no encoding here.
    # curl does not glob -d data, so the [] in the field names is sent as-is.
    set -- -X POST -H "Accept: application/json" --max-redirs 3 \
        --data-urlencode "message=$query" -d "limit=$ZLIB_SEARCH_LIMIT"
    [ -n "$ext_filter" ] && set -- "$@" -d "extensions[]=$ext_filter"
    local lang="$(zlib_language_name "$lang_filter")"
    [ -n "$lang" ] && set -- "$@" -d "languages[]=$lang"

    local mirror last_stage="" attempts=0
    for mirror in $ZLIB_URL $ZLIB_MIRROR_URLS; do
        [ -n "$mirror" ] || continue
        attempts=$((attempts + 1))
        [ "$attempts" -gt 1 ] && printf '.'

        if http_fetch "$out" "$mirror/eapi/book/search" --max-time 20 "$@"; then
            if grep -Fq '"books":[{' "$out" 2>/dev/null; then
                [ "$attempts" -gt 1 ] && printf '\n'
                ZLIB_URL="$mirror"
                ZLIB_AVAILABLE=true
                NET_FAIL_STAGE=""
                return 0
            fi
            if grep -Fq '"success":1' "$out" 2>/dev/null; then
                # A genuine answer with no matches. Another mirror serves the
                # same catalogue, so asking it would only cost time.
                last_stage="no-match"
                break
            fi
            if is_junk_response "$out"; then
                last_stage="challenge"
            else
                last_stage="empty"
            fi
        else
            last_stage="$NET_FAIL_STAGE"
        fi

        [ "$attempts" -ge 6 ] && break
    done

    [ "$attempts" -gt 1 ] && printf '\n'
    : > "$out" 2>/dev/null
    NET_FAIL_STAGE="${last_stage:-empty}"
    return 1
}

# zlib_parse_results <jsonfile>
#
# Emits the same JSON array shape as the other parsers, plus the "id" and
# "hash" that zlib_download needs.
#
# There is no jq on the device, so this pulls fields out of the response by
# key. Values are reduced to what display_books and get_json_value can carry:
# those split records on braces and stop strings at the first quote, so braces
# become parentheses and escaped quotes become apostrophes.
zlib_parse_results() {
    awk '
        function str(rec, key,   p, s) {
            p = index(rec, "\"" key "\":\"")
            if (p == 0) return ""
            s = substr(rec, p + length(key) + 4, 2000)
            # The string runs to the first quote that is not escaped.
            if (!match(s, /^([^"\\]|\\.)*/)) return ""
            s = substr(s, 1, RLENGTH)
            gsub(/\\\\/, "/", s)
            gsub(/\\"/, "\x27", s)
            gsub(/\\\//, "/", s)
            gsub(/\\[nrt]/, " ", s)
            gsub(/\\u[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]/, "?", s)
            gsub(/[{]/, "(", s)
            gsub(/[}]/, ")", s)
            gsub(/[ \t]+/, " ", s)
            sub(/^ +/, "", s)
            sub(/ +$/, "", s)
            return s
        }
        # Numbers arrive bare or quoted depending on the field.
        function num(rec, key,   p, s) {
            p = index(rec, "\"" key "\":")
            if (p == 0) return ""
            s = substr(rec, p + length(key) + 3, 24)
            sub(/^"/, "", s)
            if (!match(s, /^[0-9]+/)) return ""
            return substr(s, 1, RLENGTH)
        }

        { buf = buf $0 }

        END {
            print "["
            count = 0
            # Every book object opens with its id. Escaped text inside a value
            # cannot match, since its quotes are preceded by a backslash.
            n = split(buf, recs, "[{]\"id\":")
            for (i = 2; i <= n; i++) {
                rec = "\"id\":" recs[i]

                id = num(rec, "id")
                hash = str(rec, "hash")
                title = str(rec, "title")
                if (id == "" || hash == "" || title == "") continue

                author = str(rec, "author")
                ext = str(rec, "extension")
                md5 = str(rec, "md5")
                size = str(rec, "filesizeString")
                publisher = str(rec, "publisher")
                lang = str(rec, "language")
                year = num(rec, "year")
                if (year == "0") year = ""
                href = str(rec, "href")

                desc = publisher
                if (year != "") desc = (desc == "" ? year : desc " (" year ")")
                if (lang != "") desc = (desc == "" ? lang : desc " - " lang)
                if (ext  != "") desc = (desc == "" ? ext  : desc " - " ext)
                if (size != "") desc = (desc == "" ? size : desc " - " size)

                if (count > 0) printf ",\n"
                printf "  {\"author\": \"%s\", \"format\": \"%s\", \"md5\": \"%s\", \"title\": \"%s\", \"url\": \"%s\", \"description\": \"%s\", \"size\": \"%s\", \"id\": \"%s\", \"hash\": \"%s\", \"source\": \"zlib\"}", \
                    author, ext, md5, title, href, desc, size, id, hash
                count++
            }
            print "\n]"
        }
    ' "$1"
}
