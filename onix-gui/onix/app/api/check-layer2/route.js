import { exec } from "child_process";
import { NextResponse } from "next/server";

export async function POST(req) {
  const checked = await req.json();
  const containerName = checked.checked ? "bpp-network" : "bap-network";

  const executeShellCommand = (command) => {
    return new Promise((resolve, reject) => {
      exec(command, (error, stdout, stderr) => {
        if (error) {
          console.error("Error:", error);
          reject(error);
          return;
        }

        if (stderr) {
          console.error("Error:", stderr);
          reject(new Error(stderr));
          return;
        }

        const output = stdout;
        console.log("Output:", output);
        resolve(output);
      });
    });
  };

  try {
    const containerExists = await executeShellCommand(
      `docker ps -a --filter "name=${containerName}" --format "{{.Names}}"`
    );

    if (!containerExists.trim()) {
      return new NextResponse(`Error: ${containerName} server not present`, {
        status: 500,
      });
    }

    const result = await executeShellCommand(
      `docker exec ${containerName} ls /usr/src/app/schemas/ | grep -v "core" | grep ".yaml" | wc -l`
    );

    return new NextResponse(result);
  } catch (error) {
    console.error(`exec error: ${error}`);
    return new NextResponse(`Error executing shell command: ${error}`, {
      status: 500,
    });
  }
}
