import { exec } from "child_process";
import { NextResponse } from "next/server";

export async function POST(req) {
  const request = await req.json();
  const containerName = request.checked ? "bpp-network" : "bap-network";
  const fileToCheck = request.fileName;

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
      `docker exec ${containerName} /bin/sh -c "[ -f '/usr/src/app/schemas/${fileToCheck}' ] && echo 'File found' || echo 'File not found'"`
    );
    if (result.trim() === "File found") {
      return NextResponse.json(
        { message: true },
        {
          status: 200,
        }
      );
    } else {
      return NextResponse.json(
        { message: false },
        {
          status: 200,
        }
      );
    }
  } catch (error) {
    console.error(`exec error: ${error}`);
    return NextResponse.json(
      { message: `Error executing shell command: ${error}` },
      {
        status: 500,
      }
    );
  }
}
