import type { Problem } from "./types";

export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem | null;

  constructor(status: number, problem: Problem | null) {
    super(problem?.detail || problem?.title || `Request failed (${status})`);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }
}

function cookie(name: string): string | undefined {
  const prefix = `${encodeURIComponent(name)}=`;
  return document.cookie
    .split("; ")
    .find((item) => item.startsWith(prefix))
    ?.slice(prefix.length);
}

export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  headers.set("Accept", "application/json");
  if (options.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (options.method && !["GET", "HEAD"].includes(options.method.toUpperCase())) {
    const csrf = cookie("dockside_csrf");
    if (csrf) headers.set("X-CSRF-Token", decodeURIComponent(csrf));
  }

  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  if (response.status === 204) return undefined as T;
  if (!response.ok) {
    let problem: Problem | null = null;
    try {
      problem = (await response.json()) as Problem;
    } catch {
      // The HTTP status remains the reliable fallback.
    }
    throw new ApiError(response.status, problem);
  }
  return (await response.json()) as T;
}

export async function beginDiscord(input: {
  purpose: "login" | "claim" | "invite";
  claim_token?: string;
  invite_token?: string;
}) {
  const response = await api<{ authorization_url: string }>(
    "/api/v1/auth/discord/begin",
    { method: "POST", body: JSON.stringify(input) },
  );
  window.location.assign(response.authorization_url);
}
