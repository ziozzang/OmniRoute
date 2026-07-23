// diffeq_gen.ts — Differential-equivalence fixture generator.
//
// Runs the REAL TypeScript implementations over a systematic enumeration of
// inputs and dumps {input, expected} pairs as JSON. The Go test replays the
// exact same inputs and asserts byte-identical outputs. This is the
// ground-truth oracle (not self-referential Go expectations).
import { decideFairShare } from "./src/lib/quota/fairShare.ts";
import { computeBurnRate } from "./src/lib/quota/burnRate.ts";
import {
  quotaGroupSlug,
  quotaPoolSlug,
  quotaModelName,
  parseQuotaModelName,
  isQuotaModelName,
} from "./src/lib/quota/quotaModelNaming.ts";
import { writeFileSync } from "node:fs";

// ---------------------------------------------------------------------------
// decideFairShare — systematic branch coverage
// ---------------------------------------------------------------------------
const policies = ["hard", "soft", "burst", "weird-unknown"] as const;
// globalUsedPercent: 0.3 = generous (< 0.5), 0.7 = strict (>= 0.5)
const gups = [0.3, 0.7];
// consumed values relative to fairShare (= weight/100 * limit).
// weight=50, limit=100 → fairShare=50. Test consumed below/at/above fairShare,
// and at/above the global limit (100).
const consumedVals = [0, 25, 50, 75, 100, 150];
// consumedTotal relative to limit (100): below / at / above.
const consumedTotals = [10, 100, 120];
// cap: none / matching-unit below / matching-unit at-or-above / wrong-unit.
const capVariants = [
  { capValue: undefined, capUnit: undefined },
  { capValue: 200, capUnit: "tokens" }, // above consumed → no cap block
  { capValue: 40, capUnit: "tokens" }, // below some consumed → cap block
  { capValue: 40, capUnit: "requests" }, // wrong unit → ignored
] as const;

const fairShareCases: any[] = [];

// Empty dimensions case.
fairShareCases.push({
  input: {
    dimensions: [],
    allocation: { weight: 50, policy: "hard" },
    consumedByThisKey: {},
    saturationThreshold: 0.5,
  },
});

for (const policy of policies) {
  for (const gup of gups) {
    for (const consumed of consumedVals) {
      for (const consumedTotal of consumedTotals) {
        for (const cap of capVariants) {
          const allocation: any = { weight: 50, policy };
          if (cap.capValue !== undefined) {
            allocation.capValue = cap.capValue;
            allocation.capUnit = cap.capUnit;
          }
          fairShareCases.push({
            input: {
              dimensions: [
                {
                  key: { poolId: "p", unit: "tokens", window: "hourly" },
                  limit: 100,
                  consumedTotal,
                  globalUsedPercent: gup,
                },
              ],
              allocation,
              consumedByThisKey: { "p:tokens:hourly": consumed },
              saturationThreshold: 0.5,
            },
          });
        }
      }
    }
  }
}

// Multi-dimension case (2 dims, one blocks).
fairShareCases.push({
  input: {
    dimensions: [
      { key: { poolId: "p", unit: "tokens", window: "hourly" }, limit: 100, consumedTotal: 10, globalUsedPercent: 0.3 },
      { key: { poolId: "p", unit: "requests", window: "daily" }, limit: 10, consumedTotal: 10, globalUsedPercent: 0.3 },
    ],
    allocation: { weight: 50, policy: "hard" },
    consumedByThisKey: { "p:tokens:hourly": 5, "p:requests:daily": 5 },
    saturationThreshold: 0.5,
  },
});

const fairShareFixtures = fairShareCases.map((c) => ({
  input: c.input,
  expected: decideFairShare(c.input),
}));

// ---------------------------------------------------------------------------
// computeBurnRate — deterministic (no Date.now)
// ---------------------------------------------------------------------------
const burnHistories: Array<Array<{ ts: number; consumed: number }>> = [
  [],
  [{ ts: 0, consumed: 0 }],
  [{ ts: 0, consumed: 0 }, { ts: 1000, consumed: 100 }],
  [{ ts: 0, consumed: 0 }, { ts: 1000, consumed: 100 }, { ts: 2000, consumed: 300 }],
  [{ ts: 0, consumed: 0 }, { ts: 1000, consumed: 100 }, { ts: 2000, consumed: 150 }, { ts: 3000, consumed: 400 }],
  // duplicate / out-of-order timestamps (skipped)
  [{ ts: 0, consumed: 0 }, { ts: 0, consumed: 50 }, { ts: 1000, consumed: 100 }],
  [{ ts: 1000, consumed: 100 }, { ts: 500, consumed: 50 }], // all backwards
  // decreasing consumed (negative rate → clamped to 0)
  [{ ts: 0, consumed: 100 }, { ts: 1000, consumed: 50 }],
];
const burnRemaining: Array<number | undefined> = [undefined, 0, 500, 1000];

const burnFixtures: any[] = [];
for (const history of burnHistories) {
  for (const remaining of burnRemaining) {
    burnFixtures.push({
      input: { history, remaining: remaining === undefined ? null : remaining },
      expected: computeBurnRate(history, remaining),
    });
  }
}

// ---------------------------------------------------------------------------
// naming functions
// ---------------------------------------------------------------------------
const groupNames = [
  "Pool Principal",
  "My-Pool_2024",
  "!!!",
  "",
  "ABC123",
  "café-pool",
  "  spaced  ",
  "日本語プール",
  "a/b/c",
];
const namingFixtures: any[] = [];
for (const name of groupNames) {
  namingFixtures.push({ fn: "quotaGroupSlug", input: name, expected: quotaGroupSlug(name) });
  namingFixtures.push({ fn: "quotaPoolSlug", input: name, expected: quotaPoolSlug(name) });
  namingFixtures.push({ fn: "isQuotaModelName", input: name, expected: isQuotaModelName(name) });
}
// quotaModelName + parse round-trips
const modelCases = [
  ["Pool Principal", "codex", "gpt-5.5"],
  ["My Pool", "claude", "claude-opus-4"],
  ["grp", "openai", "ns/gpt-4o/nested"],
  ["!!!", "p", "m"],
] as const;
for (const [g, p, m] of modelCases) {
  const name = quotaModelName(g, p, m);
  namingFixtures.push({ fn: "quotaModelName", input: [g, p, m], expected: name });
  namingFixtures.push({ fn: "parseQuotaModelName", input: name, expected: parseQuotaModelName(name) });
}
// parse edge cases
const parseEdge = ["qtSd/", "qtSd/a", "qtSd/a/b", "qtSd//b/c", "qtSd/a//c", "notprefix/a/b/c", "qtSd/a/b/"];
for (const s of parseEdge) {
  namingFixtures.push({ fn: "parseQuotaModelName", input: s, expected: parseQuotaModelName(s) });
  namingFixtures.push({ fn: "isQuotaModelName", input: s, expected: isQuotaModelName(s) });
}

// ---------------------------------------------------------------------------
// Emit
// ---------------------------------------------------------------------------
const out = {
  fairShare: fairShareFixtures,
  burnRate: burnFixtures,
  naming: namingFixtures,
};
writeFileSync("./omniroute-go/quota/testdata/diffeq_fixtures.json", JSON.stringify(out, null, 2));
console.log(
  `Generated: fairShare=${fairShareFixtures.length} burnRate=${burnFixtures.length} naming=${namingFixtures.length}`,
);
