# Beckn-ONIX

Beckn-ONIX - Open Network In A Box, is a project designed to effortlessly set up and maintain Beckn network that is scalable, secure and easy to maintain.

In the install folder, you find a tool that helps install a Beckn network. This tool serves as a valuable resource for developers and network participants eager to explore Beckn protocols or join open networks supported by the Beckn protocol. By simplifying the installation process, Beckn-ONIX streamlines the onboarding experience.

Refer to the following documents for more information:

- [User Guide](./docs/user_guide.md)
- [Step by step walkthrough of a demo](./docs/demo_walkthrough.md)
- [release notes](./install/RELEASE.md)

Experience the convenience and efficiency of Beckn-ONIX as you embark on your journey with Beckn protocols and open networks.

## Note on mandatory Layer 2 Config (Important)

This note will eventually be moved to a proper place in the documentation. It has been put here to alert people who run Beckn-ONIX in the meantime.
Beckn-ONIX mandates availability of Layer 2 Config for a particular domain before any transactions can be conducted on it. If the layer 2 config is not present, on either the BAP or the BPP, the following error is returned back to the caller. "Config error : Layer 2 config not found."

Usually the network facilitators will host the Layer 2 config and provide a way to access it. Currently we have a small script (layer2/download_layer_2_config_bap.sh and layer2/download_layer_2_config_bpp.sh) that can download the layer 2 config from a fixed location and insert it into the docker container that runs the Protocol Server Client.

If you have the Layer 2 config file with you and not hosted, you can use the following procedure to update it manually. In case you do not have layer 2 config file with you, as developer machine workaround, you can copy the core_version.yaml(e.g. core_1.1.0.yaml) and rename it as the layer 2 config for a domain (e.g. for a domain named retail for core version 1.1.0, retail_1.1.0.yaml). This is strictly not recommended for production networks.

Process to manually update layer 2 config.

```
docker cp "$FILENAME" "$CONTAINER_NAME":"$CONTAINER_PATH/$FILENAME"

# example
docker cp retail_1.1.0.yaml bap-client:/usr/src/app/schemas/retail_1.1.0.yaml
docker cp retail_1.1.0.yaml bap-network:/usr/src/app/schemas/retail_1.1.0.yaml

docker cp retail_1.1.0.yaml bpp-client:/usr/src/app/schemas/retail_1.1.0.yaml
docker cp retail_1.1.0.yaml bpp-netork:/usr/src/app/schemas/retail_1.1.0.yaml

```
