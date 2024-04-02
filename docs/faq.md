## Beckn-ONIX FAQ

**Q: What is Beckn-ONIX?**
**A:** Beckn-ONIX is [FIDE](https://fide.org/) project aimed at easing setup and maintainance of a [Beckn](https://becknprotocol.io/) Network using reference implementations. Objectives include setting up reliable, configurable and fast Beckn network as a virtual appliance.

**Q: Is Beckn-ONIX a protocol? Is Beckn-ONIX a new version of Beckn?**
**A:** No Beckn-ONIX is not a protocol. Beckn-ONIX is not a new version of Beckn. Beckn-ONIX project just helps setup and maintain a Beckn network. This initiative is independent of the evolution of Beckn

**Q: What components are installed when we setup a Beckn network with Beckn-ONIX?**
**A:** When you setup a new Beckn network using Beckn-ONIX, the reference implementations of [Beckn Registry](https://github.com/beckn/beckn-registry-app), [Beckn Gateway](https://github.com/beckn/beckn-gateway-app), [BAP Beckn Adapter](https://github.com/beckn/protocol-server) and [BPP Beckn Adapter](https://github.com/beckn/protocol-server)

**Q: Do I need anything more than Beckn-ONIX to do transactions?**
**A:** Yes. Beckn-ONIX only installs the core network components. Refer to the [Sample deployment diagram](./user_guide.md/#sample-deployment-diagram) for a simple illustration of the various components. As you see in that diagram, apart from the core network components, you would need a buyer side application and a seller side application to perform transactions. Typically you will be implementing one or both of these. If you are only implementing one side of it, you can use sample applications or postman as the other application.

**Q: Do I have to use Beckn-ONIX to install these reference components?**
**A:** No. If you are comfortable you can install the reference implementations of [Beckn Registry](https://github.com/beckn/beckn-registry-app), [Beckn Gateway](https://github.com/beckn/beckn-gateway-app), [BAP Beckn Adapter](https://github.com/beckn/protocol-server) and [BPP Beckn Adapter](https://github.com/beckn/protocol-server) directly from the repositories. Beckn-ONIX makes the tasks easier through guided installation.

**Q: Do I have to use these reference components to get onto Beckn network? Can I write them myself**
**A:** Sure. As the name suggests, these are just reference components and their objective is to get you started easily. You can however write your own implementation of the Beckn protocol using the tech stack you like.

**Q: The user guide mentions Nginx for reverse proxy. Can I use any other program as reverse proxy**
**A:** Yes. You can use other programs. The user guide uses Nginx primarily as an example. The instructions to configure the reverse proxy might change. In particular, for the flow we have you might need to identify how to install SSL certificates and how to proxy a request to a port based on the requested URL.

**Q: Is Beckn-ONIX the best way to setup and manage Beckn network?**
**A:** No. Beckn-ONIX is just one effort to kick start the process of creating tools to aid easy creation and operation of Beckn networks. We want the community to create even better tools for different production environments.

**Q: How do I contribute to this project?**
**A:** We welcome contributions both to this project as well as initiatives in other production environment. Refer to the [contribution guide](./contribution.md) for more details on the process.
