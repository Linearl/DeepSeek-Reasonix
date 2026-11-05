// tl1438: consumer for use_capability action=inspect output. The backend
// inspect payload now carries a machine layer next to the prose description —
// `parameters` (per-param rows derived from the tool's registered schema) and
// `schema_note` (an explicit "no schema available" label). This module parses
// that machine layer so the tool card can render a collapsible parameter
// table; it stays pure (no React) like the rest of lib/tools.

export interface CapabilityInspectParam {
  name: string;
  type?: string;
  required?: boolean;
  enum?: unknown[];
  description?: string;
}

export interface CapabilityInspectSchema {
  params: CapabilityInspectParam[];
  // Set when the backend labelled the absence of a schema instead of
  // returning one (no fabricated data contract).
  note?: string;
}

function parseJSONObject(value: string): Record<string, unknown> | undefined {
  try {
    const parsed: unknown = JSON.parse(value);
    return parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)
      ? parsed as Record<string, unknown>
      : undefined;
  } catch {
    return undefined;
  }
}

function inspectAction(args: string): boolean {
  const a = parseJSONObject(args);
  return a?.["action"] === "inspect";
}

// capabilityInspectSchema extracts the machine layer of one inspect result.
// Returns undefined for anything that is not a use_capability inspect output
// with schema data, so non-inspect cards are untouched.
export function capabilityInspectSchema(name: string, args: string, output?: string): CapabilityInspectSchema | undefined {
  if (name !== "use_capability" || !output || !inspectAction(args)) return undefined;
  const payload = parseJSONObject(output);
  if (!payload) return undefined;
  const note = typeof payload["schema_note"] === "string" && payload["schema_note"] !== ""
    ? payload["schema_note"] as string
    : undefined;
  const rawParams = payload["parameters"];
  if (!Array.isArray(rawParams) || rawParams.length === 0) {
    return note ? { params: [], note } : undefined;
  }
  const params: CapabilityInspectParam[] = [];
  for (const row of rawParams) {
    if (row === null || typeof row !== "object" || Array.isArray(row)) continue;
    const record = row as Record<string, unknown>;
    if (typeof record["name"] !== "string" || record["name"] === "") continue;
    params.push({
      name: record["name"],
      type: typeof record["type"] === "string" ? record["type"] as string : undefined,
      required: record["required"] === true,
      enum: Array.isArray(record["enum"]) ? record["enum"] as unknown[] : undefined,
      description: typeof record["description"] === "string" ? record["description"] as string : undefined,
    });
  }
  if (params.length === 0) return note ? { params: [], note } : undefined;
  return { params, note };
}
