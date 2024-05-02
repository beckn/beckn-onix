import { NextResponse } from "next/server";

export async function POST(req, res) {
  const request = await req.json();
  const registryUrl = request.registryUrl;
  const body = { type: "BPP" };

  try {
    const response = await fetch(registryUrl, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });

    const data = await response.json();
    return NextResponse.json(data, { status: 200 });
  } catch (error) {
    console.error(error);
    return NextResponse.json(
      { error: "Error making request to registry" },
      { status: 500 }
    );
  }
}
