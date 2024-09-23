# Updating Helm Charts and Creating Releases

## Overview
This guide provides instructions on how to update Helm charts and create a new release. Follow these steps to ensure your updates are applied and released correctly.

## Prerequisites
- Helm installed and configured on your local machine.
- Access to the Helm chart repository and necessary permissions.

## Steps to Update Helm Charts

1. **Clone the Repository**
   ```bash
   git clone https://github.com/beckn/beckn-onix.git
   cd aws-cdk/helm
   ```
2. **Create a New Branch for Updates**
   ```bash 
   git checkout -b <update-branch-name>
   ```

3. Update Helm Chart
   * Navigate to the Helm chart directory: helm/registry
   * Modify the necessary files (e.g., values.yaml, templates/, Chart.yaml)

Example change in values.yaml: `replicaCount: 3`

4. Test Your Changes Locally

**Note: *** Make sure to supply necessary inputs to Helm charts with `--set` 

```bash
cd registry
helm lint registry .
helm --dry-run install registry .
helm --dry-run upgrade registry . 
```

5. Update Chart Version
* Check the current version and increment the version in Chart.yaml

```bash
version: 1.1.0
```

6. Create a Pull Request to push your changes


## Creating a Release