import { extractReasoningIntent } from "./src/lib/reasoningRouting/policy.ts";
console.log(JSON.stringify(extractReasoningIntent("claude-opus-4-high", {})));
