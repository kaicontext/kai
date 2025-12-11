#!/bin/bash
set -e

VERSION="${1:-latest}"
FILE="kai-darwin-arm64.gz"
PROJECT="rite-day%2Fivcs"
PACKAGE="kai-cli"

if [ ! -f "$FILE" ]; then
    echo "Error: $FILE not found"
    exit 1
fi

GITLAB_TOKEN=$(op read "op://Operations/65bspzotvp7kxcxum75cxo3q4i/password")

echo "Uploading $FILE to version $VERSION..."
curl --fail --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
     --upload-file "$FILE" \
     "https://gitlab.com/api/v4/projects/$PROJECT/packages/generic/$PACKAGE/$VERSION/$FILE"

echo ""
echo "Uploading $FILE to latest..."
curl --fail --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
     --upload-file "$FILE" \
     "https://gitlab.com/api/v4/projects/$PROJECT/packages/generic/$PACKAGE/latest/$FILE"

echo ""
echo "Done! Uploaded to $VERSION and latest"
