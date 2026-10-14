#!/bin/sh

update() {
	if [ "$UPDATE_AVAILABLE" = true ]; then
        echo -n "Would you like to update? [Y/n]: "
        read -r confirm

        if [ "$confirm" = "y" ] || [ "$confirm" = "Y" ] || [ -z "$confirm" ]; then
            echo "Installing update..."
            # Raw GitHub rather than Pages: the fork has no Pages site, and a
            # Pages URL would mean this fork reinstalls upstream's code.
            if curl -s --max-time 120 -A "$USER_AGENT" \
                    https://raw.githubusercontent.com/jokot/KindleFetch/main/install.sh | sh; then
                echo "Update installed successfully!"
                UPDATE_AVAILABLE=false
                VERSION=$(load_version)
                exec exit 0
            else
                echo "Failed to install update"
                sleep 2
            fi
        fi
    else
        echo "You're up-to-date!"
        sleep 2
    fi
}