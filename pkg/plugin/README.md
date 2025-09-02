# BECKN-ONIX Plugin Framework

Welcome to the BECKN-ONIX Plugin Framework! This repository provides the core components to build your own custom, modular, and extensible Beckn Onix SDK. You can pick and choose your own technology stack, whether it's multi-cloud, open-source, or your own proprietary services.

## Why Use a Plugin Framework?

We have out-of-the-box Beckn Onix SDKs available for different technology stacks. However, in case you want to further customize your setup, this framework provides the tools to do so.
It was designed by the core team of FIDE and Google Cloud engineers, with a plugin-based architecture to give you maximum flexibility.

- ✅ **Multi-Cloud & Hybrid-Cloud**: Mix and match services from different cloud providers. Use a cloud KMS for signing, your chosen cloud storage for media, and a self-hosted database.
- ✅ **Open-Source Integration**: Plug in open-source tools like Prometheus for monitoring, Jaeger for tracing, or RabbitMQ for async messaging.
- ✅ **No Vendor Lock-In**: Easily swap out a component or service provider without rebuilding your entire application.
- ✅ **Extensibility**: Add your own custom functionality by creating new plugins that meet your specific business needs.

## How It Works: Core Concepts

The framework is built on a few key components written in Go.

### Plugin Definitions

This is the contract. It's a Go library that defines the interfaces and structs for every type of plugin. For example, it defines what a Signer or a Logger must be able to do, without worrying about how it gets done. Your application code will depend on these interfaces.

### Plugin Implementations

This is the logic. An implementation is a specific tool that fulfills the contract defined in the Plugin Definitions.

- A **Default Implementation** might provide basic, sensible functionality.
- A **Custom Implementation** is one you create. For example, you could write a MyCustomSigner plugin that implements the Signer interface using your preferred signing service.
These implementations are built as standalone Go plugins (.so files) that can be dynamically loaded at runtime.

### Plugin Manager

This is the conductor. The Plugin Manager is the central access point for your application. It reads a configuration file, loads the specific plugin implementations you've chosen, initializes them, and makes them available to your application logic.

## Getting Started: Building Your Custom SDK

Let's walk through how to use the framework to build an SDK with a custom plugin.

### Prerequisites
Go (latest version recommended)
Git

### Step 1: Understand the Plugin Classes

First, familiarize yourself with the available plugin "slots" you can fill. These are defined as interfaces in the Plugin Definitions library. Core classes include:
- Cache: For in-memory storage.
- Decrypter: Handles message decryption.
- Encrypter: Handles message encryption.
- KeyManager: Manages cryptographic keys.
- Middleware: For implementing custom middleware logic.
- Publisher: Handles asynchronous message publishing.
- Registry: For looking up network participant details.
- Router: For routing requests to the correct service.
- SchemaValidator: Validates Beckn message schemas.
- Signer: Signs outgoing Beckn requests.
- SignValidator: Validates incoming request signatures.
- Step: Represents a single step in a transaction pipeline.

### Step 2: Implement a Plugin
Choose a class you want to customize. For this example, let's create a simple custom logger.

Create your plugin project:
```
mkdir my-custom-logger
cd my-custom-logger
go mod init my-custom-logger
```

Implement the interface: Create a main.go file. In this file, you'll implement the Logger interface from the Plugin Definitions package.
```
// main.go
package main

import (
    "fmt"
    "[github.com/beckn-onix/plugin-definitions](https://github.com/beckn-onix/plugin-definitions)" // Assuming this is the path
)

// MyLogger is our custom implementation.
type MyLogger struct{}

// Log fulfills the interface requirement.
func (l *MyLogger) Log(message string) error {
    fmt.Printf("MY CUSTOM LOGGER >> %s\n", message)
    return nil
}

// Export a variable that the Plugin Manager can look up.
// The variable name "Provider" is a convention.
var Provider plugin_definitions.Logger = &MyLogger{}
```

### Step 3: Configure the Plugin Manager

The Plugin Manager uses a yaml file to know which plugins to load. You'll need to update it to point to your new custom plugin.
```
# config.yaml
plugins:
  logger_plugin:
    id: "my-custom-logger" # The unique name/ID for your plugin
    # ... any config your plugin might need
  signing_plugin:
    id: "default-signer"
  # ... other plugins
```
The id here is crucial. It tells the Plugin Manager which .so file to look for.

### Step 4: Build and Run

- Build your plugin: From inside your my-custom-logger directory, run the build command. This creates the shared object file.

```
go build -buildmode=plugin -o my-custom-logger.so
```

- Place the plugin: Move the my-custom-logger.so file to a known directory where your main Beckn Onix application can find it (e.g., /plugins).
- Run your application: When your main Onix application starts, the Plugin Manager will read config.yaml, see the my-custom-logger ID, load my-custom-logger.so, and use it for all logging operations.

## Plugin Development Lifecycle

### Implementing a new Plugin for an existing Class:

- Create a new Go project for your plugin.
- Implement the required interface from the Plugin Definitions package.
- Define any configuration your plugin needs.
- Export the necessary symbols for the Plugin Manager to discover.
- Build the plugin as a .so file.

### Adding a new Plugin Class to the Framework (Advanced):
- Add the new interface and required structs to the Plugin Definitions package.
- Update the Plugin Manager to handle the loading and execution of this new plugin class.
- Update the Plugin Manager's configuration struct if needed.

## Support

If you have any questions, face any issues, or have a suggestion, please open an issue on this GitHub repository.



