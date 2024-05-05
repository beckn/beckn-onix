import { exec } from "child_process";
import { NextResponse } from "next/server";
import { promises as fs } from "fs";
import { join } from "path";
import os from "os";

const pathDir = join(os.homedir(), "beckn-onix");
async function directoryExists(path) {
  try {
    await fs.access(path);
    return true;
  } catch (error) {
    return false;
  }
}

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

export async function startSupportServices() {
  try {
    process.env.COMPOSE_IGNORE_ORPHANS = "1";

    const result1 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-app.yml up -d mongo_db`
    );
    console.log("Result 1:", result1);

    const result2 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-app.yml up -d queue_service`
    );
    console.log("Result 2:", result2);

    const result3 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-app.yml up -d redis_db`
    );
    console.log("Result 3:", result3);
    await executeCommand("docker volume create registry_data_volume");
    await executeCommand("docker volume create registry_database_volume");
    await executeCommand("docker volume create gateway_data_volume");
    await executeCommand("docker volume create gateway_database_volume");
    return NextResponse.json({ result1, result2, result3 });
  } catch (error) {
    console.error("An error occurred:", error);
    return NextResponse.json({ error: "An error occurred" }, { status: 500 });
  }
}

export async function POST(req, res) {
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

  try {
    await startSupportServices();
    const data = await req.json();
    const registryUrl = data.registryUrl;
    const bppSubscriberId = data.subscriberId;
    const bppSubscriberUrl = data.subscriberUrl;
    const webhookUrl = data.webhookUrl;
    // generating unqiuekey for bpp subscriberId
    const uniqueKeyId = bppSubscriberId + "-key";
    let updateBppConfigCommand = `bash ${pathDir}/install/scripts/update_bpp_config.sh  ${registryUrl} ${bppSubscriberId} ${uniqueKeyId} ${bppSubscriberUrl} ${webhookUrl}`;
    console.log("Update BPP Config Command:", updateBppConfigCommand);
    const result1 = await executeCommand(updateBppConfigCommand);
    console.log("Result 1:", result1);

    const result2 = await executeCommand("sleep 10");
    console.log("Result 2:", result2);

    const result3 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-v2.yml up -d  bpp-client`
    );
    console.log("Result 3:", result3);

    const result4 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-v2.yml up -d  bpp-network`
    );
    console.log("Result 4:", result4);

    const result5 = await executeCommand("sleep 10");
    console.log("Result 5:", result5);

    return NextResponse.json({ result1, result2, result3, result4, result5 });
  } catch (error) {
    console.error("An error occurred:", error);
    return NextResponse.json({ error: "An error occurred" }, { status: 500 });
  }
}
