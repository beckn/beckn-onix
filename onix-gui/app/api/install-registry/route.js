import { exec } from "child_process";
import { NextResponse } from "next/server";
import { promises as fs } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import os from "os";

async function directoryExists(path) {
  try {
    await fs.access(path);
    return true;
  } catch (error) {
    return false;
  }
}

export async function POST(req, res) {
  const pathDir = join(os.homedir(), "beckn-onix");
  const becknOnixDirExists = await directoryExists(pathDir);
  console.log("Installing Beckn Onix...", becknOnixDirExists);

  if (!becknOnixDirExists) {
    console.log(`Directory beckn-onix does not exist. Cloning repository...`);
    try {
      const response = await fetch(`${req.nextUrl.origin}/api/clonning-repo`);
      if (!response.ok) {
        console.error(
          `Failed to clone repository: ${response.status} ${response.statusText}`
        );
        return NextResponse.json(
          {
            error: `Failed to clone repository: ${response.status} ${response.statusText}`,
          },
          { status: 500 }
        );
      }
      console.log("Repository cloned successfully.");
    } catch (error) {
      console.error("An error occurred while cloning the repository:", error);
      return NextResponse.json(
        { error: "An error occurred while cloning the repository" },
        { status: 500 }
      );
    }
  }

  const data = await req.json();
  const executeCommand = (command) => {
    return new Promise((resolve, reject) => {
      exec(command, (error, stdout, stderr) => {
        if (error) {
          console.error("Error:", error);
          reject(error);
          return;
        }
        const output = stdout + stderr;
        console.log("Output:", output);
        resolve(output);
      });
    });
  };
  const updateRegistryDetails = async (url) => {
    let registryUrl = "";
    let registryPort = "";
    let protocol = "";

    if (url) {
      if (url.startsWith("https://")) {
        registryUrl = url.replace("https://", "");
        registryPort = "443";
        protocol = "https";
      } else if (url.startsWith("http://")) {
        registryUrl = url.replace("http://", "");
        registryPort = "80";
        protocol = "http";
      }
    } else {
      registryUrl = "registry";
      registryPort = "3030";
      protocol = "http";
    }

    console.log("Registry URL:", registryUrl);

    const configFile = join(
      pathDir,
      "install",
      "registry_data",
      "config",
      "swf.properties"
    );
    const sampleFile = join(
      pathDir,
      "install",
      "registry_data",
      "config",
      "swf.properties-sample"
    );

    try {
      await fs.copyFile(sampleFile, configFile);
      const tempDir = join(os.homedir(), "beckn-onix", "tmp");
      await fs.mkdir(tempDir, { recursive: true }); // Create the temporary directory if it doesn't exist

      const tempFile = join(tempDir, "tempfile.XXXXXXXXXX");
      const configData = await fs.readFile(configFile, "utf8");
      const updatedConfigData = configData
        .replace(/REGISTRY_URL/g, registryUrl)
        .replace(/REGISTRY_PORT/g, registryPort)
        .replace(/PROTOCOL/g, protocol);

      await fs.writeFile(tempFile, updatedConfigData);
      await fs.rename(tempFile, configFile);
      await executeCommand("docker volume create registry_data_volume");
      await executeCommand("docker volume create registry_database_volume");
      await executeCommand("docker volume create gateway_data_volume");
      await executeCommand("docker volume create gateway_database_volume");
      await executeCommand(
        `docker run --rm -v ${join(
          pathDir,
          "install",
          "registry_data",
          "config"
        )}:/source -v registry_data_volume:/target busybox sh -c "cp /source/envvars /target/ && cp /source/logger.properties /target/ && cp /source/swf.properties /target/"`
      );

      // Start the registry container
      await executeCommand(
        `docker-compose -f ${join(
          pathDir,
          "install",
          "docker-compose-v2.yml"
        )} up -d registry`
      );

      // Wait for 10 seconds
      await new Promise((resolve) => setTimeout(resolve, 10000));

      console.log("Registry installation successful");
    } catch (error) {
      console.error("Error updating registry details:", error);
      throw error;
    }
  };

  try {
    const url = data.registryUrl;
    await updateRegistryDetails(url);
    return NextResponse.json({
      message: "Registry details updated successfully",
    });
  } catch (error) {
    console.error("An error occurred:", error);
    return NextResponse.json({ error: "An error occurred" }, { status: 500 });
  }
}
