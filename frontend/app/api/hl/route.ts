import { type NextRequest } from "next/server";

export const dynamic = "force-dynamic";

// Server-side proxy to Hyperliquid's public info API. Keeps everything same-origin
// (no CORS surprises) and lets us point at testnet/mainnet via one env var.
const HL_INFO_URL =
  process.env.HYPERLIQUID_INFO_URL ?? "https://api.hyperliquid-testnet.xyz";

export async function POST(req: NextRequest) {
  const body = await req.text();
  try {
    const upstream = await fetch(`${HL_INFO_URL.replace(/\/$/, "")}/info`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body,
      cache: "no-store",
    });
    const text = await upstream.text();
    return new Response(text, {
      status: upstream.status,
      headers: { "content-type": "application/json" },
    });
  } catch (e) {
    return Response.json(
      { error: `failed to reach Hyperliquid: ${String(e)}` },
      { status: 502 },
    );
  }
}
