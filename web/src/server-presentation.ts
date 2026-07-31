import type { ServerSummary } from "./types";

export type StatusTone = "success" | "warning" | "danger" | "info" | "neutral";

export function serverPresentation(server: Pick<ServerSummary, "status" | "stop_reason">) {
  const { status, stop_reason: stopReason } = server;
  if (status === "running") {
    return { tone: "success" as StatusTone, label: "Running", warning: false };
  }
  if (status === "stopped") {
    const warning = Boolean(
      stopReason && stopReason !== "requested" && stopReason !== "clean_exit",
    );
    return {
      tone: "danger" as StatusTone,
      label: warning ? "Stopped ⚠" : "Stopped",
      warning,
    };
  }
  if (status === "restarting") {
    return { tone: "warning" as StatusTone, label: "Restarting", warning: false };
  }
  if (status === "stopping") {
    return { tone: "warning" as StatusTone, label: "Stopping", warning: false };
  }
  if (status === "installing") {
    return { tone: "info" as StatusTone, label: "Installing", warning: false };
  }
  if (status === "starting") {
    return { tone: "info" as StatusTone, label: "Starting", warning: false };
  }
  if (status === "failed" || status === "degraded") {
    return { tone: "danger" as StatusTone, label: status === "failed" ? "Failed" : "Degraded", warning: true };
  }
  if (status === "suspended" || status === "deleting") {
    return { tone: "warning" as StatusTone, label: status === "suspended" ? "Suspended" : "Deleting", warning: false };
  }
  return { tone: "neutral" as StatusTone, label: status, warning: false };
}
