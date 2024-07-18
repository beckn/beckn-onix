# Release Notes

## Introduction

Beckn-ONIX is [FIDE](https://fide.org/) project aimed at easing setup and maintainance of a [Beckn](https://becknprotocol.io/) Network using reference implementations. Objectives include setting up reliable, configurable and fast Beckn network as a virtual appliance. This initiative is independent of the evolution of the Beckn protocol.

Experience the convenience and efficiency of Beckn-ONIX as you embark on your journey with Beckn protocols and open networks.

| Version                                      | Release Date |
| -------------------------------------------- | ------------ |
| [v0.4.1](#beckn-onix-version-041-2024-06-22) | 2024-06-22   |
| [v0.4.0](#beckn-onix-version-040-2024-05-06) | 2024-05-06   |
| [v0.3.0](#beckn-onix-version-030-2024-03-20) | 2024-03-20   |
| [v0.2.0](#beckn-onix-version-020-2024-03-01) | 2024-03-01   |
| [v0.1.0](#beckn-onix-version-010-2024-02-16) | 2024-02-16   |

## Beckn-ONIX Version 0.4.1 (2024-06-22)

- This release adds a new feature for in CLI where its will be able to merge multiple registries.

### New Features

- Merging registry A to registry B
- Creting a new super-registry from multiple registries.

### Enhancements

- None

### Bug fixes

- None

### Limitations

- None

### Upcoming Version

- GUI support for the Merging feature.

### Release date

2024-06-22

## Beckn-ONIX Version 0.4.0 (2024-05-06)

- This release adds a new browser based GUI for installing Beckn network components

### New Features

- A new Browser based GUI installation wizard

### Enhancements

- Docker volumes used for data and config. Makes migration to new version of components easy
- New User Guide and Setup Walkthrough documents added to CLI installation

### Bug fixes

- BPP Beckn Adapter installation does not automatically install additional software (sandbox)
- Required Gateway configuration files are automatically filled (There was an error due to which this had to be manually done)

### Limitations

- GUI based installer only works on Ubuntu Linux
- There is a small error due to which the installation in progress toast for BAP and BPP does not stop, though the installation itself has succeeded
- The GUI installer needs Node, NPM and LocalTunnel to be installed before starting.

### Release date

2024-05-06

## Beckn-ONIX Version 0.3.0 (2024-03-20)

- This release supports streamlined multi-node installation.
- Support added to mandata layer 2 configuration files for the domain
- The CLI has been modified to lead the user through the journey of bringing up a Beckn Network.
- Based on whether the user is setting up a new network or joining an existing one, workflow prompt is changed

### New Features

- Multi node installation
- New CLI interface to lead user through installation
- Layer 2 Config file is mandated before transactions can happen
- Ability to download layer 2 config file from web and install in BAP/BPP Beckn Adapter

### Bug Fixes

- None

### Known Issues

- Sometimes installation fails on machines without docker due to user not getting new group membership. Running installation again fixes the issue.
- Mount binds are used instead of docker volumes. Due to this data has to be migrated before deleting the installation folder.
- BPP installs Sandbox even when not requested.

### Limitations

- The beckn-onix.sh installer used for multi node. For single node installation the start_beckn.sh file has to be used. (This is not true anymore. The beckn-onix.sh has an option to install all components on a single machine)

### Upcoming Version

- A community driven GUI installer is in works

### Release Date

- 2024-03-01

## Beckn-ONIX Version 0.2.0 (2024-03-01)

### New Features

- This release focuses on enabling the installation of individual components with user-provided configurations.
- It extends support to the Windows operating system, specifically Windows 10.
- Additionally, it now supports the Mac operating system.

This release is specifically designed to facilitate the deployment of individual components, offering users the flexibility to customize configurations. Furthermore, it ensures seamless compatibility with both Windows and Mac operating systems.

For a comprehensive summary of the features, refer [here](https://github.com/beckn/beckn-utilities/milestone/1?closed=1)

### Enhancements

- Support for Windows operating system.
- Support for Mac operating system.
- Can be used to install specific components with custom configuration.

### Bug Fixes

- None

### Known Issues

- None

### Limitations

- The current installer is tested only for Linux (Ubuntu) / Windows (windows 10) / Mac, it might support other flavors also.
- The current version supports only vertical scaling, horizontal scaling (ECS / EKS) is planned for an upcoming release
- When installing individual components, registration with the registry has to be done manually, this is explicitly done to avoid confusion and to prevent the network from incorrect or wrong registrations.

### Upcoming Version

- Support for horizontal scaling using Elastic Kubernetes Cluster.

### Release Date

- 2024-03-01

## Beckn-ONIX Version 0.1.0 (2024-02-16)

### Objective

Beckn-ONIX - Open Network In A Box, is a utility designed to effortlessly set up all Beckn components on a machine using a one-click installer. This tool serves as a valuable resource for developers and network participants eager to explore Beckn protocols or join open networks supported by the Beckn protocol. By simplifying the installation process, Beckn-ONIX streamlines the onboarding experience.

The current version installs all components automatically without requiring user input, facilitating a seamless setup process. However, we are committed to further enhancing Beckn-ONIX's functionality. In the upcoming release, we will introduce the capability to selectively install specific components and accommodate user-provided configurations.

For a comprehensive summary of the features, refer [here](https://github.com/beckn/beckn-utilities/milestone/2?closed=1)

Experience the convenience and efficiency of Beckn-ONIX as you embark on your journey with Beckn protocols and open networks.

### New Features

- Implemented installation support for the following Beckn components:
  - Protocol Server BAP
  - Protocol Server BPP
  - Webhook BPP
  - Sandbox
  - Registry
  - Gateway
  - Infrastructure required for the above services

This release is specifically tailored for deployment on Linux machines, encompassing all aforementioned components with default configurations.

### Enhancements

- None

### Bug Fixes

- None

### Known Issues

- None

### Limitations

- None

### Upcoming Version

- Installation of individual components with user-provided configuration.
- Support for Windows and Mac OS will be added.

### Release Date

- 2024-02-16
