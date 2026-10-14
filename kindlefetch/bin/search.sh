#!/bin/sh

display_books() {
    clear
    echo -e "
  _____                     _     
 / ____|                   | |    
| (___   ___  __ _ _ __ ___| |__  
 \___ \ / _ \/ _\` | '__/ __| '_ \\ 
 ____) |  __/ (_| | | | (__| | | |
|_____/ \___|\__,_|_|  \___|_| |_|
"
    echo "--------------------------------"
    echo ""

    local books="$1"
    local page="$2"
    local has_prev="$3"
    local has_next="$4"
    local last_page="$5"

    local count
    count="$(echo "$books" | grep -o '"title":' | wc -l)"

    local display_index=1
    local start=$(( (page - 1) * RESULTS_PER_PAGE ))
    local end=$(( start + RESULTS_PER_PAGE - 1 ))
    [ "$end" -ge "$count" ] && end=$((count - 1))

    i=$((end))
    while [ "$i" -ge "$start" ]; do
        book_info="$(echo "$books" | awk -v i=$i 'BEGIN{RS="\\{"; FS="\\}"} NR==i+2{print $1}')"

        title="$(get_json_value "$book_info" "title")"
        author="$(get_json_value "$book_info" "author")"
        format="$(get_json_value "$book_info" "format")"
        description="$(get_json_value "$book_info" "description")"

        if [ "$COMPACT_OUTPUT" != true ]; then
            printf "%2d. %s\n" "$((i+1))" "$title"
            [ -n "$description" ] && [ "$description" != "null" ] && echo "    $description"
            echo ""
        else
            printf "%2d. %s by %s in %s format\n" \
                "$((i+1))" "$title" "$author" "$format"
            echo ""
        fi

        display_index=$((display_index + 1))
        i=$((i - 1))
    done

    local items_on_page=$(( end - start + 1 ))

    echo "--------------------------------"
    echo ""
    echo "Page $page of $last_page"
    echo ""

    [ "$has_prev" = true ] && echo -n "p: Previous page | "
    echo -n "t[1-$last_page]: Select page | "
    [ "$has_next" = true ] && echo -n "n: Next page | "
    echo "1-$items_on_page: Select book | q: Quit"
    echo ""
}

# Explain why a search produced nothing.
#
# Upstream printed an empty list whether the fetch failed, a challenge page came
# back, or the query genuinely had no matches - which is why the "no results"
# bug reports were never actionable. Naming the stage costs nothing and makes
# the next report diagnosable.
search_report_failure() {
    local q="$1"
    echo ""
    case "${NET_FAIL_STAGE:-empty}" in
        dns)
            echo "Search failed: could not resolve any mirror."
            echo "Check the Wi-Fi connection, or try Settings > Toggle Cloudflare DNS."
            ;;
        tls)
            echo "Search failed: the TLS handshake was rejected."
            echo "This device's certificate bundle is probably too old for these sites."
            ;;
        timeout)
            echo "Search failed: every mirror timed out."
            ;;
        challenge)
            echo "Search failed: the mirrors answered with a bot-protection challenge"
            echo "(DDoS-Guard / Cloudflare) that this tool cannot solve."
            ;;
        nginx-default)
            echo "Search failed: mirrors returned a placeholder page instead of Library Genesis."
            echo "This is a mirror-side fault; trying again usually reaches a working one."
            ;;
        http-*)
            echo "Search failed: mirror returned HTTP ${NET_FAIL_STAGE#http-}."
            ;;
        empty)
            echo "No results returned for '$q'."
            echo "Libgen mirrors answer inconsistently, so searching again often works."
            ;;
        no-match)
            echo "No results returned for '$q'."
            echo "Try fewer words, or clear the format and language filters."
            ;;
        *)
            echo "Search failed: $NET_FAIL_STAGE"
            ;;
    esac
    echo ""
    sleep 3
}

search_books() {
    local query="$1"
    local page="${2:-1}"
    
    if [ -z "$query" ]; then
        echo -n "Enter search query: "
        read -r query
        [ -z "$query" ] && {
            echo "Search query cannot be empty"
            return 1
        }
    fi
    
    echo "Searching for '$query' (page $page)..."

    local filters=""
    if [ -f "$SCRIPT_DIR"/tmp/current_filter_params ]; then
        filters=$(cat "$SCRIPT_DIR/tmp/current_filter_params")
    fi
    
    local encoded_query="$(urlencode "$query")"
    local html_file="$TMP_DIR/.kf_search.$$"
    local source_kind=""

    # Z-Library's app API first: its website is behind DiamWall, but /eapi/
    # answers curl with JSON. Then Libgen, which serves plain HTML when it is
    # up. Anna's Archive last - it returns 403 (DDoS-Guard) on /search from
    # every known mirror, even though its homepage answers 200, which is what
    # made this look like "no results" rather than a network failure.
    if zlib_search "$query" "$html_file"; then
        source_kind="zlib"
        # zlib_search sets ZLIB_URL to whichever mirror answered; remember it.
        save_config
    elif [ "$NET_FAIL_STAGE" = "no-match" ]; then
        # Z-Library answered and has nothing. Its catalogue already includes
        # Libgen's, so the fallbacks would only add their timeouts.
        :
    else
        echo "Z-Library returned nothing; trying Libgen..."
        if lgli_search "$encoded_query" "$page" "$html_file"; then
            source_kind="lgli"
            # lgli_search sets LGLI_URL to whichever mirror answered; remember it so
            # the next launch starts from a host known to have worked.
            save_config
        elif [ -n "$ANNAS_URL" ]; then
            # A long shot while the block is in place, but it costs one request and
            # means the app starts working again by itself if that ever changes.
            echo "Libgen returned nothing; trying Anna's Archive..."
            if http_fetch "$html_file" \
                    "$ANNAS_URL/search?page=${page}&q=${encoded_query}${filters}" --max-time 12 \
               && ! is_junk_response "$html_file" \
               && grep -Fq 'href="/md5/' "$html_file" 2>/dev/null; then
                source_kind="annas"
            elif [ -z "$NET_FAIL_STAGE" ]; then
                NET_FAIL_STAGE="challenge"
            fi
        fi
    fi

    if [ -z "$source_kind" ]; then
        rm -f "$html_file"
        search_report_failure "$query"
        return 1
    fi

    local books
    if [ "$source_kind" = "zlib" ]; then
        books="$(zlib_parse_results "$html_file")"
    elif [ "$source_kind" = "lgli" ]; then
        books="$(lgli_parse_results "$html_file" "$LGLI_URL")"
    else
        # Reading from the file rather than `echo $html_content`: unquoted, that
        # word-split and glob-expanded the entire HTML document.
        books="$(awk -v base_url="$ANNAS_URL" '
        BEGIN {
            RS = "<div class=\"flex pt-3 pb-3 border-b last:border-b-0 border-gray-100\">"
            print "["
            count = 0
        }
        NR > 1 {
            title = ""; author = ""; md5 = ""; format = ""; description = ""
        
            # md5. Avoids the /{32}/ interval form, which mawk and some
            # busybox builds do not support and silently fail to match.
            if (match($0, /href="\/md5\/[a-f0-9]+"/)) {
                md5 = substr($0, RSTART+11, RLENGTH-12)
                if (length(md5) != 32) md5 = ""
            }
        
            # title
            if (match($0, /<div class="font-bold text-violet-900 line-clamp-\[5\]" data-content="[^"]+"/)) {
                block = substr($0, RSTART, RLENGTH)
                if (match(block, /data-content="[^"]+"/)) {
                    title = substr(block, RSTART+14, RLENGTH-15)
                }
            }
        
            # author
            if ($0 ~ /<div[^>]*class="[^"]*font-bold[^"]*text-amber-900[^"]*line-clamp-\[2\][^"]*"/) {
                if (match($0, /<div[^>]*class="[^"]*font-bold[^"]*text-amber-900[^"]*line-clamp-\[2\][^"]*" data-content="[^"]+"/)) {
                    block = substr($0, RSTART, RLENGTH)
                    if (match(block, /data-content="[^"]+"/)) {
                        author = substr(block, RSTART+14, RLENGTH-15)
                    }
                }
            }
        
            # format
            if (match($0, /<div class="text-gray-800[^>]*>[^<]+/)) {
                line = substr($0, RSTART, RLENGTH)
                if (match(line, />[^<]+/)) {
                    content = substr(line, RSTART+1, RLENGTH-1)
                    n = split(content, parts, " · ")
                    if (n >= 2) {
                        format = parts[2]
                    }
                }
            }
            
            # description
            if (match($0, /<div[^>]*class="[^"]*text-gray-800[^"]*font-semibold[^"]*text-sm[^"]*leading-\[1\.2\][^"]*mt-2[^"]*"[^>]*>.*?<\/div>/)) {
                line = substr($0, RSTART, RLENGTH)

                gsub(/<script[^>]*>[^<]*(<[^>]*>[^<]*)*<\/script>/, "", line)

                gsub(/<a[^>]*>[^<]*(<[^>]*>[^<]*)*<\/a>/, "", line)

                gsub(/<[^>]*>/, "", line)

                gsub(/&[#a-zA-Z0-9]+;/, "", line)

                gsub(/^[ \t\r\n]+|[ \t\r\n]+$/, "", line)

                description = line
            }
        
            # emoji replacements
            gsub(/🚀/, "Partner Server", description)
            gsub(/📗|📘|📕|📰|💬|📝|🤨|🎶|✅/, "", description)
        
            # escape double quotes
            gsub(/"/, "\\\"", title)
            gsub(/"/, "\\\"", author)
            gsub(/"/, "\\\"", description)
        
            if (title != "") {
                if (count > 0) {
                    printf ",\n"
                }
                printf "  {\"author\": \"%s\", \"format\": \"%s\", \"md5\": \"%s\", \"title\": \"%s\", \"url\": \"%s/md5/%s\", \"description\": \"%s\", \"source\": \"annas\"}", author, format, md5, title, base_url, md5, description
                count++
            }
        }
        END {
            print "\n]"
        }' "$html_file")"
    fi

    local last_page
    if [ "$source_kind" = "zlib" ]; then
        # One request returned every result, so the page count is ours to work
        # out from what was parsed.
        local result_count="$(echo "$books" | grep -o '"title":' | wc -l)"
        last_page=$(( (result_count + RESULTS_PER_PAGE - 1) / RESULTS_PER_PAGE ))
        [ "$last_page" -lt 1 ] && last_page=1
    else
        last_page="$(lgli_last_page "$html_file")"
    fi

    rm -f "$html_file"

    local has_prev=false
    [ "$page" -gt 1 ] && has_prev=true

    local has_next=false
    [ "$page" -lt "$last_page" ] && has_next=true

    echo "$query" > "$TMP_DIR"/last_search_query
    echo "$page" > "$TMP_DIR"/last_search_page
    echo "$last_page" > "$TMP_DIR"/last_search_last_page
    echo "$has_next" > "$TMP_DIR"/last_search_has_next
    echo "$has_prev" > "$TMP_DIR"/last_search_has_prev

    echo "$books" > "$TMP_DIR"/search_results.json

    while true; do
        local query="$(cat "$TMP_DIR"/last_search_query 2>/dev/null)"
        local current_page="$(cat "$TMP_DIR"/last_search_page 2>/dev/null || echo 1)"
        local last_page="$(cat "$TMP_DIR"/last_search_last_page 2>/dev/null || echo 1)"
        local has_next="$(cat "$TMP_DIR"/last_search_has_next 2>/dev/null || echo "false")"
        local has_prev="$(cat "$TMP_DIR"/last_search_has_prev 2>/dev/null || echo "false")"
        local books="$(cat "$TMP_DIR"/search_results.json 2>/dev/null)"
        local count="$(echo "$books" | grep -o '"title":' | wc -l)"

        display_books "$books" "$current_page" "$has_prev" "$has_next" "$last_page"
        
        echo -n "Enter choice: "
        read -r choice
        
        case "$choice" in
            [qQ])
                return 0
                ;;
            [pP])
                if [ "$has_prev" = true ]; then
                    new_page=$((current_page - 1))
                    echo "$new_page" > "$TMP_DIR"/last_search_page
                    has_prev="$([ "$new_page" -gt 1 ] && echo "true" || echo "false")"
                    has_next="$([ "$new_page" -lt "$last_page" ] && echo "true" || echo "false")"
                    echo "$has_prev" > "$TMP_DIR"/last_search_has_prev
                    echo "$has_next" > "$TMP_DIR"/last_search_has_next
                    continue
                else
                    echo "Already on first page"
                    sleep 2
                fi
                ;;
            [nN])
                if [ "$has_next" = true ]; then
                    new_page=$((current_page + 1))
                    echo "$new_page" > "$TMP_DIR"/last_search_page
                    has_prev="$([ "$new_page" -gt 1 ] && echo "true" || echo "false")"
                    has_next="$([ "$new_page" -lt "$last_page" ] && echo "true" || echo "false")"
                    echo "$has_prev" > "$TMP_DIR"/last_search_has_prev
                    echo "$has_next" > "$TMP_DIR"/last_search_has_next
                    continue
                else
                    echo "Already on last page"
                    sleep 2
                fi
                ;;
            t[0-9]*)
                page_number="${choice#t}"
                if echo "$page_number" | grep -qE '^[0-9]+$'; then
                    if [ "$page_number" -ge 1 ] && [ "$page_number" -le "$last_page" ]; then
                        if [ "$page_number" -ne "$current_page" ]; then
                            echo "$page_number" > "$TMP_DIR"/last_search_page
                            has_prev="$([ "$page_number" -gt 1 ] && echo "true" || echo "false")"
                            has_next="$([ "$page_number" -lt "$last_page" ] && echo "true" || echo "false")"
                            echo "$has_prev" > "$TMP_DIR"/last_search_has_prev
                            echo "$has_next" > "$TMP_DIR"/last_search_has_next
                            continue
                        else
                            echo "You are already on page $current_page"
                            sleep 2
                        fi
                    else
                        echo "Page number out of range (1-$last_page)"
                        sleep 2
                    fi
                else
                    echo "Invalid input"
                    sleep 2
                fi
                ;;
            *)  
                if echo "$choice" | grep -qE '^[0-9]+$'; then
                    local start=$(( (current_page - 1) * RESULTS_PER_PAGE ))
                    local end=$(( start + RESULTS_PER_PAGE - 1 ))
                    [ "$end" -ge "$count" ] && end=$((count - 1))
                    local items_on_page=$(( end - start + 1 ))

                    if [ "$choice" -ge 1 ] && [ "$choice" -le "$count" ]; then
                        absolute_index=$(( choice - 1 ))

                        book_info="$(awk -v i=$absolute_index \
                            'BEGIN{RS="\\{"; FS="\\}"} NR==i+2{print $1}' \
                            "$TMP_DIR"/search_results.json)"

                        local lgli_available=false
                        local zlib_available=false

                        # Route on the record's own source field. The previous
                        # substring test looked for "lgli"/"zlib" anywhere in the
                        # record, which no Libgen record ever contains - so no
                        # result could reach a downloader at all.
                        case "$book_info" in
                            *'"source": "zlib"'*)
                                zlib_available=true
                                # The record carries its md5 too, so Libgen can
                                # serve it when it is up - useful once the daily
                                # Z-Library download limit is spent.
                                lgli_available=true
                                ;;
                            *'"source": "lgli"'*)
                                lgli_available=true
                                ;;
                            *'"source": "annas"'*)
                                # An Anna's record is a content hash, which the
                                # Libgen downloader can resolve directly.
                                lgli_available=true
                                [ "$ZLIB_AVAILABLE" = true ] && zlib_available=true
                                ;;
                        esac

                        while true; do
                            if [ "$lgli_available" = false ] && [ "$zlib_available" = false ]; then
                                echo "There are no available sources for this book right now."
                            fi

                            if [ "$lgli_available" = true ]; then
                                echo "1. lgli"
                            fi
                            if [ "$zlib_available" = true ]; then
                                if [ "$ZLIB_AUTH" = true ]; then
                                    echo "2. zlib"
                                else
                                    echo "2. zlib (Authentication required)"
                                fi
                            fi
                            echo "3. Cancel download"

                            echo -n "Choose source to proceed with: "
                            read -r source_choice

                            case "$source_choice" in
                                1)
                                    if [ "$lgli_available" = true ]; then
                                        echo "Proceeding with lgli..."
                                        if ! lgli_download "$choice"; then
                                            echo "Download from lgli failed."
                                            sleep 2
                                        else
                                            break
                                        fi
                                    else
                                        echo "Invalid choice."
                                    fi
                                    ;;
                                2)
                                    if [ "$zlib_available" = true ]; then
                                        if [ "$ZLIB_AUTH" = true ]; then
                                            echo "Proceeding with zlib..."
                                            if ! zlib_download "$choice"; then
                                                echo "Download from zlib failed."
                                                sleep 2
                                            else
                                                break
                                            fi
                                        else
                                            echo
                                            echo -n "Do you want to sign into your zlib account? [Y/n]: "
                                            read -r zlib_login_choice
                                            echo

                                            if [ "$zlib_login_choice" = "n" ] || [ "$zlib_login_choice" = "N" ]; then
                                                ZLIB_AUTH=false
                                                save_config
                                            else
                                                while true; do
                                                    echo -n "Zlib email: "
                                                    read -r zlib_email
                                                    echo -n "Zlib password: "
                                                    read -r zlib_password
                                                    echo

                                                    if zlib_login "$zlib_email" "$zlib_password"; then
                                                        ZLIB_AUTH=true
                                                        save_config

                                                        printf "\n\nProceeding with zlib..."
                                                        if ! zlib_download "$choice"; then
                                                            echo "Download from zlib failed."
                                                            sleep 2
                                                        else
                                                            break 2
                                                        fi
                                                    else
                                                        echo -n "Zlib login failed. Do you want to try again? [Y/n]: "
                                                        read -r zlib_login_retry_choice
                                                        echo
                                                        
                                                        if [ "$zlib_login_retry_choice" = "n" ] || [ "$zlib_login_retry_choice" = "N" ]; then
                                                            ZLIB_AUTH=false
                                                            save_config
                                                            break
                                                        fi
                                                    fi
                                                done
                                            fi
                                        fi
                                    else
                                        echo "Invalid choice."
                                    fi
                                    ;;
                                3)
                                    break
                                    ;;
                                *)
                                    echo "Invalid choice."
                                    ;;
                            esac
                        done

                        # busybox read supports neither -n nor -s, so upstream's
                        # `read -n 1 -s` fails with "invalid option" on-device.
                        printf "\nPress Enter to continue..."
                        read -r _continue
                    else
                        echo "Invalid selection (must be between 1 and $items_on_page)"
                        sleep 2
                    fi
                else
                    echo "Invalid input"
                    sleep 2
                fi
                ;;
        esac
    done
}
