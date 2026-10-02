#!/usr/bin/env node

import { readFile } from "node:fs/promises";

function shapeReport(entries) {
  const observations = entries.flatMap((entry, index) => summarizeEntry(entry, index));
  return { source: "local HAR; no requests replayed", matching_request_count: observations.length, observations };
}

function summarizeEntry(entry, index) {
  try {
    const requestURL = new URL(entry?.request?.url);
    if (requestURL.protocol !== "https:" || !isFandangoHost(requestURL.hostname)) return [];
    const contentType = String(entry?.response?.content?.mimeType || "").split(";", 1)[0];
    return [{
      index,
      method: String(entry?.request?.method || "UNKNOWN").toUpperCase(),
      host: requestURL.hostname,
      path: sanitizedPath(requestURL.pathname),
      query_parameter_names: [...new Set([...requestURL.searchParams.keys()])].sort(),
      status: Number(entry?.response?.status || 0),
      content_type: contentType,
      response_shape: responseShape(entry?.response?.content),
      known_role: knownRole(requestURL.pathname),
    }];
  } catch {
    return [];
  }
}

function isFandangoHost(hostname) {
  const normalized = hostname.toLowerCase();
  return normalized === "fandango.com" || normalized.endsWith(".fandango.com");
}

function knownRole(pathname) {
  if (pathname.startsWith("/napi/seatMap/")) return "read-only seat-map preview";
  if (pathname.startsWith("/napi/theaterShowtimeGroupings/")) return "movie showtime discovery";
  if (pathname.startsWith("/napi/theaterMovieShowtimes/")) return "theater showtime discovery";
  if (pathname.startsWith("/napi/theaterCalendar/")) return "theater showtime calendar";
  if (/checkout|purchase|payment|cart|ticketing|reserv|seat/i.test(pathname)) return "transactional path";
  return "unclassified";
}

function sanitizedPath(pathname) {
  for (const prefix of [
    "/napi/seatMap/",
    "/napi/theaterShowtimeGroupings/",
    "/napi/theaterMovieShowtimes/",
    "/napi/theaterCalendar/",
  ]) {
    if (pathname.startsWith(prefix)) return `${prefix}{id}`;
  }
  return pathname;
}

function responseShape(content) {
  const text = responseText(content);
  if (!text || text.length > 5_000_000) return null;
  try {
    return shapeOf(JSON.parse(text), 0);
  } catch {
    return null;
  }
}

function responseText(content) {
  if (typeof content?.text !== "string") return "";
  if (content.encoding === "base64") return Buffer.from(content.text, "base64").toString("utf8");
  return content.text;
}

function shapeOf(value, depth) {
  if (depth >= 3) return Array.isArray(value) ? "array" : value === null ? "null" : typeof value;
  if (Array.isArray(value)) return { type: "array", length: value.length, item: value.length ? shapeOf(value[0], depth + 1) : null };
  if (value && typeof value === "object") {
    const keys = Object.keys(value).sort().slice(0, 60);
    return { type: "object", keys, fields: Object.fromEntries(keys.map((key) => [key, shapeOf(value[key], depth + 1)])) };
  }
  return value === null ? "null" : typeof value;
}

// --flow: a chronological, redacted transcript of the seat-select → hold → release sequence.
// Personal values are dropped outright. Credentials and opaque identifiers become stable aliases
// (`<v3 len=36>`), so a value minted in one response can be traced into later requests without
// printing it. Seat labels, statuses, counts, and other short plain values are kept.

const credentialKey = /token|csrf|xsrf|session|auth|secret|signature|cookie|nonce|verification|viewstate|eventvalidation|bearer/i;
const emailValue = /[^\s@]+@[^\s@]+\.[^\s@]+/;
const personalKey = /e-?mail|phone|mobile|first.?name|last.?name|full.?name|display.?name|^name$|user|holder|customer|password|card|cvv|cvc|expir|billing|address|street|zip|postal|birth|loyalty|member|account/i;
const staticTypes = new Set(["image", "stylesheet", "font", "media", "script", "manifest", "ping"]);
const staticExtension = /\.(?:png|jpe?g|gif|svg|webp|avif|ico|css|js|mjs|map|woff2?|ttf|otf|mp4|webm)$/i;

function flowReport(entries) {
  const aliases = new Map();
  const alias = (value) => {
    if (!aliases.has(value)) aliases.set(value, `v${aliases.size + 1}`);
    return `<${aliases.get(value)} len=${value.length}>`;
  };
  const tokenLike = (value) => value.length >= 20 && /^[\w+/=.:%-]+$/.test(value) && /\d/.test(value) && /[a-z]/i.test(value);

  const redactString = (key, value) => {
    if (personalKey.test(key) || emailValue.test(value)) return "<personal>";
    if (credentialKey.test(key) || tokenLike(value)) return alias(value);
    if (value.length > 80) return `<text len=${value.length}>`;
    return value;
  };
  const redact = (value, key = "") => {
    if (typeof value === "string") return redactString(key, value);
    if (typeof value === "number") return personalKey.test(key) ? "<personal>" : value;
    if (Array.isArray(value)) {
      const items = value.slice(0, 12).map((item) => redact(item, key));
      return value.length > 12 ? [...items, `<+${value.length - 12} more>`] : items;
    }
    if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([name, item]) => [name, redact(item, name)]));
    return value;
  };
  const redactPairs = (pairs) => Object.fromEntries(pairs.map(([name, value]) => [name, redactString(name, value)]));

  const body = (text, mimeType) => {
    if (!text) return undefined;
    const type = String(mimeType || "").split(";", 1)[0].trim().toLowerCase();
    try {
      if (type.includes("json") || /^\s*[[{]/.test(text)) return { json: redact(JSON.parse(text)) };
    } catch { /* fall through */ }
    if (type === "application/x-www-form-urlencoded") return { form: redactPairs([...new URLSearchParams(text)]) };
    if (type.includes("html")) return { html_forms: htmlForms(text, redactString), bytes: text.length };
    return { type, bytes: text.length };
  };

  const timeOf = (entry) => Date.parse(entry?.startedDateTime ?? "") || 0;
  const ordered = entries.map((entry, index) => ({ entry, index })).sort((a, b) => timeOf(a.entry) - timeOf(b.entry) || a.index - b.index);
  const started = ordered.length ? timeOf(ordered[0].entry) : 0;
  const otherHosts = {};
  const steps = [];
  for (const { entry, index } of ordered) {
    let url;
    try { url = new URL(entry?.request?.url); } catch { continue; }
    if (!isFandangoHost(url.hostname)) {
      otherHosts[url.hostname] = (otherHosts[url.hostname] ?? 0) + 1;
      continue;
    }
    if (staticTypes.has(entry?._resourceType) || staticExtension.test(url.pathname)) continue;
    const phase = url.searchParams.get("centerseat_phase");
    const at_ms = timeOf(entry) - started;
    if (phase) {
      steps.push({ at_ms, phase });
      continue;
    }
    const headers = Array.isArray(entry?.request?.headers) ? entry.request.headers : [];
    const cookieHeader = headers.filter((header) => /^cookie$/i.test(header?.name)).map((header) => String(header.value)).join("; ");
    const responseHeaders = Array.isArray(entry?.response?.headers) ? entry.response.headers : [];
    steps.push({
      at_ms,
      index,
      method: String(entry?.request?.method || "UNKNOWN").toUpperCase(),
      host: url.hostname,
      path: url.pathname.split("/").map((segment) => tokenLike(segment) ? alias(segment) : segment).join("/"),
      query: redactPairs([...url.searchParams]),
      request_header_names: [...new Set(headers.map((header) => String(header?.name || "").toLowerCase()).filter((name) => name && !name.startsWith(":")))].sort(),
      credential_headers: redactPairs(headers.filter((header) => credentialKey.test(header?.name ?? "") && !/^cookie$/i.test(header.name)).map((header) => [header.name, String(header.value)])),
      cookie_names: cookieHeader ? [...new Set(cookieHeader.split(/;\s*/).map((pair) => pair.split("=", 1)[0]).filter(Boolean))].sort() : [],
      request_body: body(entry?.request?.postData?.text ?? (entry?.request?.postData?.params ? new URLSearchParams(entry.request.postData.params.map((param) => [param.name, param.value ?? ""])).toString() : ""), entry?.request?.postData?.mimeType),
      status: Number(entry?.response?.status || 0),
      redirect: entry?.response?.redirectURL ? redactURL(entry.response.redirectURL, redactPairs, alias, tokenLike) : undefined,
      set_cookie_names: responseHeaders.filter((header) => /^set-cookie$/i.test(header?.name)).map((header) => String(header.value).split("=", 1)[0]).sort(),
      response_body: body(responseText(entry?.response?.content), entry?.response?.content?.mimeType),
    });
  }
  return {
    source: "local HAR; no requests replayed; values redacted, opaque values aliased",
    step_count: steps.length,
    other_hosts: otherHosts,
    steps,
  };
}

function redactURL(raw, redactPairs, alias, tokenLike) {
  try {
    const url = new URL(raw, "https://www.fandango.com");
    return { host: url.hostname, path: url.pathname.split("/").map((segment) => tokenLike(segment) ? alias(segment) : segment).join("/"), query: redactPairs([...url.searchParams]) };
  } catch {
    return "<unparseable>";
  }
}

// ASP.NET checkout pages post back hidden form state; report each form's target and fields.
function htmlForms(html, redactString) {
  const forms = [];
  for (const match of html.matchAll(/<form\b([^>]*)>([\s\S]*?)<\/form>/gi)) {
    const attribute = (source, name) => source.match(new RegExp(`\\b${name}\\s*=\\s*["']([^"']*)["']`, "i"))?.[1] ?? "";
    const fields = {};
    for (const input of match[2].matchAll(/<(?:input|select|textarea)\b([^>]*)>/gi)) {
      const name = attribute(input[1], "name");
      if (name) fields[name] = redactString(name, attribute(input[1], "value"));
    }
    forms.push({ method: attribute(match[1], "method").toUpperCase() || "GET", action: attribute(match[1], "action").split("?", 1)[0], fields });
  }
  return forms.slice(0, 10);
}

// Runs last so the module-level patterns above are initialised before either report uses them.
const args = process.argv.slice(2);
const flow = args.includes("--flow");
const path = args.find((arg) => !arg.startsWith("--"));
if (!path) {
  console.error("Usage: npm run research:fandango-har -- [--flow] /absolute/path/to/capture.har");
  process.exitCode = 2;
} else {
  const parsed = JSON.parse(await readFile(path, "utf8"));
  const entries = Array.isArray(parsed?.log?.entries) ? parsed.log.entries : [];
  console.log(JSON.stringify(flow ? flowReport(entries) : shapeReport(entries), null, 2));
}

