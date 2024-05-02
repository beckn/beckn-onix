import { spawn } from "child_process";
import { NextResponse } from "next/server";
import os from "os";
import path from "path";
// This function is used to clone the github repository of beckn-onix
export async function GET(req) {
  console.log("Cloning GitHub repository...");
  const repoUrl = "https://github.com/beckn/beckn-onix";
  const destination = path.join(os.homedir(), "beckn-onix");
  const gitProcess = spawn("git", ["clone", repoUrl, destination]);

  gitProcess.stdout.on("data", (data) => {
    console.log(`stdout: ${data}`);
  });

  gitProcess.stderr.on("data", (data) => {
    console.error(`stderr: ${data}`);
  });

  return new Promise((resolve, reject) => {
    gitProcess.on("close", (code) => {const destination = "~/beckn-onix";
      if (code === 0) {
        console.log("Repository cloned successfully");
        resolve(
          NextResponse.json(
            { success: true, data: "Repo Cloned Successfully" },
            { status: 200 }
          )
        );
      } else {
        console.error(`git process exited with code ${code}`);
        resolve(NextResponse.json({ success: false }, { status: 500 }));
      }
    });
  });
}
