/**
 * alice-auto — OpenCode plugin for alice coordination (server-side hooks).
 *
 * Uses the real OpenCode plugin API:
 *   - "chat.message" hook → captures user task prompt from message parts
 *   - event hook → registers on "session.created", publishes on "session.idle"
 *   - "shell.env" hook → injects ALICE_SERVER_URL into shell environment
 *
 * This plugin guarantees alice is never skipped — something always gets
 * published, even without AGENTS.md. The agent adds quality standup summaries
 * per AGENTS.md.
 *
 * Requires the alice CLI binary at bin/alice (built from cmd/alice/).
 * Set ALICE_SERVER_URL to the coordination server URL to enable.
 *
 * Config via environment variables:
 *   ALICE_SERVER_URL          — coordination server URL (required to enable)
 *   ALICE_PLUGIN_ORG_SLUG     — org slug for registration (default: "demo")
 *   ALICE_PLUGIN_OWNER_EMAIL  — owner email for registration (default: "demo@example.com")
 *   ALICE_PLUGIN_AGENT_NAME   — agent name for registration (default: "opencode-agent")
 */

import type { Hooks, PluginInput } from "@opencode-ai/plugin";

const ALICE_BIN = "./bin/alice";

let aliceReady = false;
let lastUserPrompt = "";
let serverUrl = "";

async function hasAliceSession($: PluginInput["$"]): Promise<boolean> {
  const result = await $`${ALICE_BIN} --json --server ${serverUrl} whoami`
    .nothrow()
    .quiet();
  if (result.exitCode !== 0) return false;
  try {
    const data = JSON.parse(result.stdout.toString());
    return !!data.agent_id;
  } catch {
    return false;
  }
}

async function registerAlice($: PluginInput["$"]): Promise<boolean> {
  const orgSlug = process.env.ALICE_PLUGIN_ORG_SLUG || "demo";
  const ownerEmail = process.env.ALICE_PLUGIN_OWNER_EMAIL || "demo@example.com";
  const agentName = process.env.ALICE_PLUGIN_AGENT_NAME || "opencode-agent";

  const result =
    await $`${ALICE_BIN} --json --server ${serverUrl} register \
      --org ${orgSlug} \
      --email ${ownerEmail} \
      --agent ${agentName}`
      .nothrow()
      .quiet();

  if (result.exitCode !== 0) {
    return false;
  }
  return true;
}

async function publishStatus(
  $: PluginInput["$"],
  summary: string,
): Promise<void> {
  const title = `Working on: ${summary}`;
  const content =
    "Auto-published baseline from OpenCode session. Agent was working on the task described in the title. Check agent-published standup summaries for detail.";

  await $`${ALICE_BIN} --json --server ${serverUrl} publish \
    --type status_delta \
    --title ${title} \
    --content ${content} \
    --confidence 1.0`.nothrow().quiet();
}

export async function server(input: PluginInput): Promise<Hooks> {
  const url = (process.env.ALICE_SERVER_URL || "").replace(/\/$/, "");
  serverUrl = url;

  const { $ } = input;

  const binCheck = await $`test -x ${ALICE_BIN}`.nothrow();
  const hasBin = binCheck.exitCode === 0;

  // Always return hooks even without bin/url — they fire, just skip
  // alice operations.  This ensures hooks are never silently absent.
  return {
    "shell.env": async (_, output) => {
      if (url) {
        output.env = { ...output.env, ALICE_SERVER_URL: url };
      }
    },

    "chat.message": async (_, output) => {
      if (output.message.role === "user") {
        const text = output.parts
          .filter((p: any) => p.type === "text")
          .map((p: any) => p.text)
          .join(" ");
        if (text) {
          lastUserPrompt = text;
        }
      }
    },

    event: async ({ event }) => {
      switch (event.type) {
        case "session.created": {
          if (!url || !hasBin || aliceReady) return;
          if (await hasAliceSession(input.$)) {
            aliceReady = true;
            return;
          }
          aliceReady = await registerAlice(input.$);
          break;
        }
        case "session.idle": {
          if (!aliceReady || !lastUserPrompt) return;
          await publishStatus(input.$, lastUserPrompt.slice(0, 80));
          lastUserPrompt = "";
          break;
        }
      }
    },
  };
}
