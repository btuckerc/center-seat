#!/usr/bin/env node

import { readFile } from "node:fs/promises";

const path = process.argv[2];
if (!path) {
  console.error("Usage: npm run research:fandango-har -- /absolute/path/to/capture.har");
  process.exitCode = 2;
} else {
  const parsed = JSON.parse(await readFile(path, "utf8"));
  const entries = Array.isArray(parsed?.log?.entries) ? parsed.log.entries : [];
  const observations = entries.flatMap((entry, index) => summarizeEntry(entry, index));
  console.log(JSON.stringify({
    source: "local HAR; no requests replayed",
    matching_request_count: observations.length,
    observations,
  }, null, 2));
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
  if (/checkout|purchase|payment|cart|ticketing/i.test(pathname)) return "transactional path — out of scope";
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
