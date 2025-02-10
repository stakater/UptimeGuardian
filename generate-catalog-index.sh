DOCKER_REPO=$1
OPERATOR_NAME=$2
CATALOG_DIR_PATH=$3
VERSION=$4
PR_TAG=$5

# Get entries and iterate
CHANNEL_BUNDLES=$(yq eval-all 'select(.schema == "olm.channel") | .entries[].name' "$CATALOG_DIR_PATH"/channels.yaml | grep -v '^---$' | sort | uniq)
# Render all required bundles
rm -rf "$CATALOG_DIR_PATH"/bundles.yaml

echo " catalog build start"
SHOULD_RELEASE="false"
for item in $CHANNEL_BUNDLES; do
  # Setup bundle from entries
  if [ -n "$PR_TAG" ]; then
      bundle="${item//${OPERATOR_NAME}./${OPERATOR_NAME}-bundle:}${PR_TAG}"
      release="${OPERATOR_NAME}-bundle:v${VERSION}${PR_TAG}"
  else
      bundle="${item//${OPERATOR_NAME}./${OPERATOR_NAME}-bundle:}"
      release="${OPERATOR_NAME}-bundle:v${VERSION}"
  fi

  # Check if next release is defined in any channel
  if [ "${bundle}" == "${release}" ]; then
      SHOULD_RELEASE="true"
  else
      SHOULD_RELEASE="false"
  fi

  opm render "$DOCKER_REPO/$bundle" --output=yaml >> "$CATALOG_DIR_PATH"/bundles.yaml
  echo "   >> rendered $bundle >> $CATALOG_DIR_PATH/bundles.yaml"
done

# Build catalog index if there should be a release
  if [ ${SHOULD_RELEASE} = "true" ]; then
      yq eval-all '.' "$CATALOG_DIR_PATH"/package.yaml "$CATALOG_DIR_PATH"/channels.yaml "$CATALOG_DIR_PATH"/bundles.yaml > "$CATALOG_DIR_PATH"/release/index.yaml
      echo "  >> created index >> $CATALOG_DIR_PATH/release/index.yaml"
  else
      echo "  >> release is not defined in ${CATALOG_DIR_PATH}/channels.yaml, will not create catalog index"
  fi

rm -rf "$CATALOG_DIR_PATH"/bundles.yaml
echo " catalog build done!"

## Delete entries
#yq eval 'del(select(.schema == "olm.bundle"))' -i catalog/index.yaml
