DOCKER_REPO=$1
OPERATOR_NAME=$2
CATALOG_DIR_PATH=$3

# Get entries and iterate
CHANNEL_BUNDLES=$(yq eval-all 'select(.schema == "olm.channel") | .entries[].name' $CATALOG_DIR_PATH/channels.yaml | grep -v '^---$' | sort | uniq)
# Render all required bundles
rm -rf $CATALOG_DIR_PATH/bundles.yaml
echo " catalog build start"
for item in $CHANNEL_BUNDLES; do
  bundle="${item//${OPERATOR_NAME}./${OPERATOR_NAME}-bundle:}"
  opm render "$DOCKER_REPO/$bundle" --output=yaml >> $CATALOG_DIR_PATH/bundles.yaml
  echo "   >> rendered $bundle >> $CATALOG_DIR_PATH/bundles.yaml"
done

# Build catalog index
yq eval-all '.' $CATALOG_DIR_PATH/package.yaml $CATALOG_DIR_PATH/channels.yaml $CATALOG_DIR_PATH/bundles.yaml > $CATALOG_DIR_PATH/release/index.yaml
echo "  >> created index >> $CATALOG_DIR_PATH/release/index.yaml"

rm -rf $CATALOG_DIR_PATH/bundles.yaml
echo " catalog build done!"

## Delete entries
#yq eval 'del(select(.schema == "olm.bundle"))' -i catalog/index.yaml
