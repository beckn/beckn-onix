import { exec } from "child_process";
import { NextResponse } from "next/server";
import { promises as fs } from "fs";
import { join } from "path";
import os from "os";

const pathDir = join(os.homedir(), "beckn-onix");

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

async function directoryExists(path) {
  try {
    await fs.access(path);
    return true;
  } catch (error) {
    return false;
  }
}
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

export async function POST(req) {
  const becknOnixDirExists = await directoryExists(pathDir);

  if (!becknOnixDirExists) {
    console.log(`Directory "${pathDir}" does not exist. Cloning repository...`);
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
    const bapSubscriberId = data.subscriberId;
    const bapSubscriberUrl = data.subscriberUrl;
    const networkconfigurl = data.networkconfigurl;
    // generating unqiuekey for bap subscriberId
    const uniqueKeyId = bapSubscriberId + "-key";

    let updateBapConfigCommand = `bash ${pathDir}/install/scripts/update_bap_config.sh  ${registryUrl} ${bapSubscriberId} ${uniqueKeyId}  ${bapSubscriberUrl} ${networkconfigurl}`;
    const result1 = await executeCommand(updateBapConfigCommand);
    console.log("Result 1:", result1);
    const result3 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-v2.yml up -d  "bap-client"`
    );
    console.log("Result 3:", result3);

    const result4 = await executeCommand(
      `docker-compose -f ${pathDir}/install/docker-compose-v2.yml up -d  "bap-network"`
    );
    console.log("Result 4:", result4);

    return NextResponse.json({ result1, result3, result4 });
  } catch (error) {
    console.error("An error occurred:", error);
    return NextResponse.json({ error: "An error occurred" }, { status: 500 });
  }
}
