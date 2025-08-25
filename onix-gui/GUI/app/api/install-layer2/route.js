import { exec } from "child_process";
import { NextResponse } from "next/server";

export async function POST(req) {
  const request = await req.json();
  const fileURL = request.yamlUrl;
  const containerName = request.container;

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
    await executeShellCommand(
      `docker exec ${
        containerName + "-client"
      } wget -P /usr/src/app/schemas/ ${fileURL}`
    );
  } catch (error) {
    console.error(`exec error: ${error}`);
  }

  try {
    await executeShellCommand(
      `docker exec ${
        containerName + "-network"
      } wget -P /usr/src/app/schemas/ ${fileURL}`
    );
    return NextResponse.json({ status: 200 });
  } catch (error) {
    console.error(`exec error: ${error}`);
    return NextResponse.json({ status: 500 });
  }
}