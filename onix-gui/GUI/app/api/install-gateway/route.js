import { exec } from "child_process";
import { NextResponse } from "next/server";
import { promises as fs } from "fs";
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

  try {
    const result1 = await executeCommand(
      `bash ${pathDir}/install/scripts/package_manager.sh`
    );
    console.log("Result 1:", result1);
    await executeCommand("docker volume create registry_data_volume");
    await executeCommand("docker volume create registry_database_volume");
    const result2 = await executeCommand(
      ` bash ${pathDir}/install/scripts/update_gateway_details.sh ${data.registryUrl} ${data.gatewayUrl}`
    );
    console.log("Result 2:", result2);

    const result3 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-v2.yml up -d gateway`
    );
    console.log("Result 3:", result3);

    const result4 = await executeCommand(`sleep 2`);
    console.log("Result 4:", result4);

    const result5 = await executeCommand(
      `bash ${pathDir}/install/scripts/register_gateway.sh ${data.gatewayUrl}`
    );
    console.log("Result 5:", result5);

    return NextResponse.json({ result1, result2, result3, result4, result5 });
  } catch (error) {
    console.error("An error occurred:", error);
    return NextResponse.json({ error: "An error occurred" }, { status: 500 });
  }
}
