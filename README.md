# Uptime Guardian Operator
UptimeGuardian is an OpenShift operator which runs in the Hub cluster and watches `Routes` in the Spoke clusters and creates corresponding Prometheus `Probes` in the Hub cluster.

## Description
UptimeGuardian is designed to operate in a hub-spoke architecture, where it is installed on the hub (mothershift) cluster. The operator performs the following key functions:

1. Watches `HostedCluster` Custom Resources (CRs) in the hub cluster to discover and maintain connections with spoke (childshift) clusters
2. Establishes secure connections to the spoke clusters using the credentials and configuration from the `HostedCluster` CRs
3. Monitors `Routes` in the spoke clusters based on configured label selectors
4. Creates and manages corresponding Prometheus `Probe` resources in the hub cluster

To use UptimeGuardian, you need to:
1. Install the operator in your hub cluster
2. Create an `UptimeProbe` CR in the hub cluster with configuration including:
   - Label selectors to identify which Routes to monitor in the spoke clusters
   - Target namespace where the Prometheus Probes will be created in the hub cluster
   - Any additional monitoring configuration like intervals, timeouts, etc.

The operator automatically handles the synchronization between spoke cluster Routes and hub cluster Probes, ensuring your routes are continuously monitored for availability.

## Getting Started

### Prerequisites
- go version v1.23.0+
- docker version 20.10+.
- kubectl version v1.23.0+.
- Access to a OpenShift cluster v4.14+

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=ghcr.io/stakater/uptimeguardian:tag
```

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=ghcr.io/stakater/uptimeguardian:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

### Development

**Running the operator locally:**

Ensure you are connected to the Hub cluster and your kubeconfig is set to use the Hub cluster context.
Install the CRDs as mentioned in the [To Deploy on the cluster](#to-deploy-on-the-cluster) section and run the operator locally.

```sh
make run
```

**Running E2E Tests:**
Use the provided VSCode launch configuration to execute the end-to-end tests. The launch file contains the necessary configuration for running tests in your development environment. `Debug E2E Tests` is the launch configuration for running the end-to-end tests. It have environment variables which can be used to configure the test. To run the test in VSCode, you need to select the test name and then click on the `Debug E2E Tests` launch configuration.

If you want to run the test outside of VSCode, you can run the following command (make sure you have all the environment variables set mentioned in the launch configuration `launch.json`):

```sh
make test-e2e
```

**Running Unit Tests:**
Apart from using the VSCode launch configuration (by selecting the any unit test name as regex and then clicking on the `Debug Unit Tests` launch configuration), you can also run the unit tests using the following command:

```sh
make test
```

**Creating Release**
Refer to [this](DEPLOY.md) if you want to make a release.

## Contributing to UptimeGuardian!

Before submitting a pull request:
1. Ensure all tests pass locally
2. Add tests / e2e tests for any new features
3. Update documentation as needed
4. Follow the existing code style and conventions

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

