# Deployment
## Manual release flow
For local development testing operator by pushing directly to Docker image repository.
1. Change `VERSION` and `DOCKER_REPO_BASE` variable in Makefile
2. Build controller and bundle images
   - `make manifests build docker-build docker-push`
   - `make bundle bundle-build bundle-push`
3. Add version entry to `catalog/channels` according to OLM upgrade specifications
   - Add `skips` for entry for non-breaking API changes
     ```
     entries:
      - name: {{NAME}}-operator.v0.0.3
        skips:
          - {{NAME}}-operator.v0.0.1
          - {{NAME}}-operator.v0.0.2
          - ....
      ```
   - Add `replaces` for entry for breaking API changes
       ```
       entries:
         - name: {{NAME}}-operator.v0.0.3
          replaces: {{NAME}}-operator.v0.0.1
       ```
4. Render, build and push bundle to the catalog index
   - `make catalog-render catalog-build catalog-push`

## CI release flow
For pipeline build and release of images and most importantly the custom catalog.
Version will be bumped from `existing release tag`, not what is set in Makefile unlike manual release.

### Catalog release will only occur when following conditions are met
* Changes are made to files in `catalog` directory
* `Next version` entry are specified in `catalog/channels.yaml`
* `Next version` is bumped from existing release version according to semver, for example v0.0.1 will be v0.0.2

1. Add next version entry to `catalog/channels` according to OLM upgrade specifications.
   ```
      entries:
         - name: uptimeguardian.v{{CURRENT}}
         - name: uptimeguardian.v{{NEXT}}
            skips:
            - uptimeguardian.v0.0.1
   ```
2. Make PR, test PR release
3. Merge will build and push releases

#### More information
https://docs.openshift.com/container-platform/4.17/operators/admin/olm-managing-custom-catalogs.html
