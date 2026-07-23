import { decideFairShare } from "./src/lib/quota/fairShare.ts";
import { computeBurnRate } from "./src/lib/quota/burnRate.ts";
import { quotaGroupSlug, quotaModelName, parseQuotaModelName } from "./src/lib/quota/quotaModelNaming.ts";

const d = decideFairShare({
  dimensions: [{ key: { poolId: "p", unit: "tokens", window: "hourly" }, limit: 100, consumedTotal: 10, globalUsedPercent: 0.6 }],
  allocation: { weight: 50, policy: "hard" },
  consumedByThisKey: { "p:tokens:hourly": 60 },
  saturationThreshold: 0.5,
});
console.log("fairShare:", JSON.stringify(d));
console.log("burnRate:", JSON.stringify(computeBurnRate([{ts:0,consumed:0},{ts:1000,consumed:100}], 500)));
console.log("slug:", quotaGroupSlug("Pool Principal!"));
console.log("modelName:", quotaModelName("Pool Principal", "codex", "gpt-5.5"));
console.log("parse:", JSON.stringify(parseQuotaModelName("qtSd/poolprincipal/codex/gpt-5.5")));
