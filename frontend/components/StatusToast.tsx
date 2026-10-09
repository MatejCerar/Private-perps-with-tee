"use client";

import { css, C } from "@/lib/ui";
import { explorerTx } from "@/lib/config";

export type RunKind = "deposit" | "withdraw" | "trade" | "close" | "cancel";
export type RunPhase = "signing" | "mining" | "relaying" | "releasing" | "done" | "error";

export type RunState = {
  phase: RunPhase;
  stepReached: number; // index of the current (or last-reached, on error) step
  kind: RunKind;
  title: string;
  sub: string;
  txHash?: `0x${string}`;
  instructionId?: `0x${string}`;
  resultJson?: string;
  error?: string;
  detailsOpen: boolean;
};

const BASE_STEPS = ["Sign in your wallet", "Confirming on Flare", "Executing on Hyperliquid"];

export function StatusToast({
  run,
  hlExplorerUrl,
  onToggleDetails,
  onDismiss,
}: {
  run: RunState;
  hlExplorerUrl?: string;
  onToggleDetails: () => void;
  onDismiss: () => void;
}) {
  const done = run.phase === "done";
  const error = run.phase === "error";
  const terminal = done || error;

  // Withdrawal has a final on-Coston release step (executeWithdraw).
  const steps =
    run.kind === "withdraw" ? [...BASE_STEPS, "Release on Coston"] : BASE_STEPS;

  function stepState(i: number): "done" | "active" | "todo" | "fail" {
    if (done) return "done";
    if (error) return i < run.stepReached ? "done" : i === run.stepReached ? "fail" : "todo";
    return i < run.stepReached ? "done" : i === run.stepReached ? "active" : "todo";
  }

  const border = done
    ? "rgba(97,184,84,0.4)"
    : error
      ? "rgba(238,76,69,0.4)"
      : "rgba(255,255,255,0.1)";

  const iconWrap =
    "flex-shrink:0;width:30px;height:30px;border-radius:9px;display:flex;align-items:center;justify-content:center;font-size:14px;font-weight:800;" +
    (done
      ? `background:${C.green};color:#1a1a1a;animation:hypePop .4s ease;`
      : error
        ? `background:${C.red};color:#1a1a1a;animation:hypePop .4s ease;`
        : `border:2px solid ${C.accent};border-top-color:transparent;animation:hypeSpin .8s linear infinite;`);

  const pipe: { label: string; reached: boolean }[] = [
    { label: "submitted", reached: !!run.txHash },
    { label: "mined", reached: !!run.instructionId },
    { label: "relayed", reached: !!run.instructionId },
    { label: "executed", reached: done },
  ];

  return (
    <div
      style={css(
        `position:fixed;right:22px;bottom:22px;z-index:70;width:384px;max-width:calc(100vw - 44px);border-radius:16px;border:1px solid ${border};background:linear-gradient(180deg,#232323,#1a1a1a);box-shadow:0 30px 70px -24px rgba(0,0,0,0.9);overflow:hidden;animation:hypeRise 0.28s ease;`,
      )}
    >
      <div style={css("display:flex;align-items:flex-start;gap:10px;padding:14px 16px;border-bottom:1px solid rgba(255,255,255,0.06);")}>
        <div style={css(iconWrap)}>{done ? "✓" : error ? "!" : ""}</div>
        <div style={css("flex:1;min-width:0;")}>
          <div
            style={css(
              "font-size:13.5px;font-weight:800;color:#f6f6f6;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-bottom:1px;",
            )}
          >
            {run.title}
          </div>
          <div
            style={css(
              error
                ? "font-size:12.5px;color:#f2726c;line-height:1.45;word-break:break-word;"
                : "font-size:11.5px;color:#777777;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;",
            )}
          >
            {done ? (
              <>
                Done
                {hlExplorerUrl && (
                  <>
                    {" · "}
                    <a
                      href={hlExplorerUrl}
                      target="_blank"
                      rel="noreferrer"
                      style={css("color:#f73c68;font-weight:700;text-decoration:none;")}
                    >
                      View on HL ↗
                    </a>
                  </>
                )}
              </>
            ) : error ? (
              run.error || "Failed"
            ) : (
              run.sub
            )}
          </div>
        </div>
        {terminal && (
          <button
            onClick={onDismiss}
            style={css(
              "width:26px;height:26px;border-radius:8px;border:1px solid rgba(255,255,255,0.1);background:transparent;color:#777777;font-size:13px;cursor:pointer;",
            )}
          >
            ✕
          </button>
        )}
      </div>

      <div style={css("padding:12px 16px;display:flex;flex-direction:column;gap:9px;")}>
        {steps.map((label, i) => {
          const st = stepState(i);
          const dotStyle =
            "flex-shrink:0;width:18px;height:18px;border-radius:50%;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:800;" +
            (st === "done"
              ? `background:${C.green};color:#1a1a1a;`
              : st === "fail"
                ? `background:${C.red};color:#1a1a1a;`
                : st === "active"
                  ? `border:2px solid ${C.accent};border-top-color:transparent;animation:hypeSpin .8s linear infinite;`
                  : "border:2px solid #232323;");
          const labelStyle =
            "font-size:12.5px;font-weight:" +
            (st === "active" ? "800" : "600") +
            ";color:" +
            (st === "todo" ? "#414141" : "#d1d1d1") +
            ";";
          const showLink = i === 1 && !!run.txHash;
          return (
            <div key={i} style={css("display:flex;align-items:center;gap:10px;")}>
              <span style={css(dotStyle)}>{st === "done" ? "✓" : st === "fail" ? "!" : ""}</span>
              <span style={css(labelStyle)}>{label}</span>
              {showLink && (
                <a
                  href={explorerTx(run.txHash!)}
                  target="_blank"
                  rel="noreferrer"
                  style={css("margin-left:auto;font-size:11px;font-weight:700;")}
                >
                  explorer ↗
                </a>
              )}
            </div>
          );
        })}
      </div>

      <button
        onClick={onToggleDetails}
        style={css(
          "display:flex;align-items:center;justify-content:center;gap:6px;width:100%;padding:9px;border:none;border-top:1px solid rgba(255,255,255,0.06);background:transparent;color:#777777;font-size:11.5px;font-weight:700;cursor:pointer;",
        )}
      >
        Details{" "}
        <span
          style={css(
            "display:inline-block;transition:transform .2s;" +
              (run.detailsOpen ? "transform:rotate(180deg);" : ""),
          )}
        >
          ▾
        </span>
      </button>

      {run.detailsOpen && (
        <div
          style={css(
            "padding:12px 16px 14px;background:rgba(0,0,0,0.3);border-top:1px solid rgba(255,255,255,0.06);font-family:var(--font-jetbrains),ui-monospace,monospace;",
          )}
        >
          <div style={css("display:flex;flex-direction:column;gap:7px;font-size:11px;")}>
            {pipe.map((q) => (
              <div
                key={q.label}
                style={css(
                  "display:flex;align-items:center;gap:8px;color:" +
                    (q.reached ? C.green : "#414141") +
                    ";",
                )}
              >
                <span>{q.reached ? "✔" : "·"}</span>
                <span style={css("color:#777777;")}>{q.label}</span>
              </div>
            ))}
          </div>
          <div
            style={css(
              "margin-top:11px;padding-top:11px;border-top:1px solid rgba(255,255,255,0.07);font-size:10.5px;line-height:1.7;color:#414141;",
            )}
          >
            <div>
              instructionId{" "}
              <span style={css("color:#f73c68;word-break:break-all;")}>
                {run.instructionId ?? "—"}
              </span>
            </div>
            <div>
              flareTx <span style={css("color:#f73c68;word-break:break-all;")}>{run.txHash ?? "—"}</span>
            </div>
            {run.resultJson && (
              <div
                style={css(
                  "white-space:pre-wrap;word-break:break-all;margin-top:6px;color:#777777;",
                )}
              >
                {run.resultJson}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
