# Updating Helm Charts and Creating Releases

## Overview
This guide provides instructions on how to update Helm charts and create a new release. Follow these steps to ensure your updates are applied and released correctly.

If AWS CDK is used to provision the Beckn-ONIX services, new version of Helm chart needs to be configured in AWS CDK properties as well.

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

### Packaging Helm Charts

1. Package the Helm Chart Navigate to the Helm chart directory and create a package of the chart.

```bash
helm package registry
```
This command will create a .tgz file in the current directory.

2. Move the Package to the Packages Folder Move the generated package file to a packages folder located parallel to the helm folder:

```bash
mv registry-1.1.0.tgz ../packages/
```

### Creating an Index File

1. Navigate to the `packages` directory and generate an index file that contains metadata about the packaged Helm charts.

```bash
cd ../packages
helm repo index . --url https://github.com/beckn/beckn-onix/packages
```
This command creates an `index.yaml` file in the packages directory.

### Pushing Changes to GitHub

1. Add Changes to Git Stage the newly created package and index file for commit.

```bash
git add ../packages/registry-1.1.0.tgz ../packages/index.yaml
git commit -m "Add Helm chart version 1.1.0 and update index file"
git push origin <update-branch-name>
```

2. Create a Pull Request Go to your GitHub repository and create a pull request for the changes.

