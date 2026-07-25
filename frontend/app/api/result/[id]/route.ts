import { type NextRequest } from "next/server";

export const dynamic = "force-dynamic";

// Server-side proxy to the TEE proxy's GET /action/result/{id}. Keeps the
// upstream URL server-only and sidesteps browser CORS. `id` is the 0x-prefixed
// instructionId, forwarded verbatim (the proxy expects the 0x prefix).
//
// Status contract with the client poller (lib/tee.ts):
//   - 503 { code:"NO_PROXY" } → definitive config error, stop polling.
//   - 400                     → definitive (malformed id), stop polling.
//   - 200                     → an ActionResponse (may be pending, status=2).
//   - 502 { error }           → upstream error/unreachable — transient, keep
//                               polling; `error` carries the real upstream body
//                               so a persistent failure surfaces the actual cause
//                               (e.g. a proxy 500 while a slow close settles).
export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;

  const proxy = process.env.TEE_PROXY_URL;
  if (!proxy) {
    return Response.json(
      { error: "TEE_PROXY_URL is not configured on the server", code: "NO_PROXY" },
      { status: 503 },
    );
  }

  if (!/^0x[0-9a-fA-F]{64}$/.test(id)) {
    return Response.json({ error: "invalid instruction id" }, { status: 400 });
  }

  const url = `${proxy.replace(/\/$/, "")}/action/result/${id}`;
  try {
    const upstream = await fetch(url, { cache: "no-store" });
    const body = await upstream.text();
    if (upstream.ok) {
      return new Response(body, {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }
    // Normalize any upstream non-2xx to a retryable 502 and forward the real
    // body as `error` so the poller can surface it if it persists.
    return Response.json(
      {
        error: (body || upstream.statusText || `upstream ${upstream.status}`).slice(0, 600),
        upstreamStatus: upstream.status,
      },
      { status: 502 },
    );
  } catch (e) {
    return Response.json(
      { error: `failed to reach TEE proxy: ${String(e)}` },
      { status: 502 },
    );
  }
}
