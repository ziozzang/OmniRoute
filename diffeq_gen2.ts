// diffeq_gen2.ts — Extended differential-equivalence fixture generator.
// Covers planRegistry + accountBuckets (saturationSignals excluded: imports @/ logger).
import { getKnownPlan, knownProviders } from "./src/lib/quota/planRegistry.ts";
import {
  isBucketSaturated,
  recordUsage,
  updateAccountBuckets,
  _clearBucketsForTest,
  _bucketCountForTest,
} from "./src/lib/quota/accountBuckets.ts";
import { readFileSync, writeFileSync } from "node:fs";

const FIXTURE_PATH = "./omniroute-go/quota/testdata/diffeq_fixtures.json";
const existing = JSON.parse(readFileSync(FIXTURE_PATH, "utf-8"));

// ---------------------------------------------------------------------------
// planRegistry
// ---------------------------------------------------------------------------
const providers = [...knownProviders(), "unknown", "", "CODEX", "claude", "grok-cli"];
const planRegistryFixtures: any[] = [];
for (const p of providers) {
  planRegistryFixtures.push({ fn: "getKnownPlan", input: p, expected: getKnownPlan(p) });
}
planRegistryFixtures.push({ fn: "knownProviders", input: null, expected: knownProviders() });

// ---------------------------------------------------------------------------
// accountBuckets — stateful, driven with explicit nowMs
// ---------------------------------------------------------------------------
const NOW = 1_700_000_000_000;
const FUTURE = NOW + 3_600_000;
const PAST = NOW - 3_600_000;

interface BucketOp { fn: "recordUsage" | "isBucketSaturated" | "updateAccountBuckets"; args: any[]; }
interface BucketCase { ops: BucketOp[]; expected: { saturated: Record<string, boolean>; count: number }; }

function runBucketCase(ops: BucketOp[]): BucketCase["expected"] {
  _clearBucketsForTest();
  const saturated: Record<string, boolean> = {};
  for (const op of ops) {
    if (op.fn === "recordUsage") recordUsage(op.args[0], op.args[1], op.args[2], op.args[3], op.args[4]);
    else if (op.fn === "isBucketSaturated") saturated[`${op.args[0]}::${op.args[1]}`] = isBucketSaturated(op.args[0], op.args[1], op.args[2]);
    else if (op.fn === "updateAccountBuckets") updateAccountBuckets(op.args[0], op.args[1], op.args[2]);
  }
  return { saturated, count: _bucketCountForTest() };
}

const bucketCases: BucketOp[][] = [
  [{ fn: "recordUsage", args: ["c1", "5h", 50, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 100, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 150, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 100, new Date(PAST).toISOString(), NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 100, new Date(FUTURE).toISOString(), NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 100, new Date(FUTURE).toISOString(), NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", FUTURE] }],
  [{ fn: "updateAccountBuckets", args: ["c1", { quotas: { "session (5h)": { used: 100, total: 100, resetAt: null } } }, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "updateAccountBuckets", args: ["c1", { quotas: { "weekly (7d)": { used: 100, total: 100, resetAt: null } } }, NOW] }, { fn: "isBucketSaturated", args: ["c1", "7d", NOW] }],
  [{ fn: "updateAccountBuckets", args: ["c1", { quotas: { "weekly designer (7d)": { used: 100, total: 100, resetAt: null } } }, NOW] }, { fn: "isBucketSaturated", args: ["c1", "7d:designer", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 100, null, NOW] }, { fn: "updateAccountBuckets", args: ["c1", { quotas: { "weekly (7d)": { used: 50, total: 100, resetAt: null } } }, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", NaN, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", Infinity, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["", "5h", 100, null, NOW] }, { fn: "isBucketSaturated", args: ["", "5h", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "", 100, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 100, null, NOW] }, { fn: "recordUsage", args: ["c1", "7d", 50, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }, { fn: "isBucketSaturated", args: ["c1", "7d", NOW] }],
  [{ fn: "recordUsage", args: ["c1", "5h", 100, null, NOW] }, { fn: "recordUsage", args: ["c1", "5h", 50, null, NOW] }, { fn: "isBucketSaturated", args: ["c1", "5h", NOW] }],
];

const bucketFixtures = bucketCases.map((ops) => ({ ops, expected: runBucketCase(ops) }));

// ---------------------------------------------------------------------------
// Merge + emit
// ---------------------------------------------------------------------------
existing.planRegistry = planRegistryFixtures;
existing.buckets = bucketFixtures;
writeFileSync(FIXTURE_PATH, JSON.stringify(existing, null, 2));
console.log(`Extended: planRegistry=${planRegistryFixtures.length} buckets=${bucketFixtures.length}`);
console.log(`Total: ${JSON.stringify(Object.fromEntries(Object.entries(existing).map(([k, v]) => [k, (v as any[]).length])))}`);
