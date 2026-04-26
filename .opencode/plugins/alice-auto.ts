/**
 * alice-auto — OpenCode plugin for alice coordination.
 *
 * Hooks into session lifecycle events to:
 *   - Register with alice on session start (if not already registered)
 *   - Publish status_delta on session idle as a baseline guarantee
 *
 * The agent adds quality standup summaries per AGENTS.md.
 * This plugin ensures alice is never skipped — something always gets published.
 *
 * Requires the alice CLI binary at bin/alice (built from cmd/alice/).
 * Set ALICE_SERVER_URL to enable. No env vars needed for graceful skip.
 *
 * Config via environment variables:
 *   ALICE_SERVER_URL          — coordination server URL (required to enable)
 *   ALICE_PLUGIN_ORG_SLUG     — org slug for registration (default: "demo")
 *   ALICE_PLUGIN_OWNER_EMAIL  — owner email for registration (default: "demo@example.com")
 *   ALICE_PLUGIN_AGENT_NAME   — agent name for registration (default: "opencode-agent")
 */

const ALICE_BIN = "./bin/alice";

let aliceReady = false;
let taskPrompt = "";
let taskStarted = false;

async function hasAliceSession($: Shell): Promise<boolean> {
  const result =
    await $`${ALICE_BIN} --json whoami`.quiet().nothrow();
  if (result.exitCode !== 0) return false;
  try {
    const data = JSON.parse(result.text());
    return !!data.agent_id;
  } catch {
    return false;
  }
}

async function registerAlice($: Shell, serverUrl: string) {
  const orgSlug = process.env.ALICE_PLUGIN_ORG_SLUG || "demo";
  const ownerEmail = process.env.ALICE_PLUGIN_OWNER_EMAIL || "demo@example.com";
  const agentName = process.env.ALICE_PLUGIN_AGENT_NAME || "opencode-agent";

  const result =
    await $`${ALICE_BIN} --json register --org ${orgSlug} --email ${ownerEmail} --agent ${agentName} --server ${serverUrl}`
      .quiet()
      .nothrow();

  if (result.exitCode !== 0) {
    console.error("alice-auto: register failed:", result.stderr.toString());
    return false;
  }
  return true;
}

interface Shell {
  (pieces: TemplateStringsArray, ...args: any[]): Promise<{
    exitCode: number;
    text(): string;
    stderr: { toString(): string };
    quiet(): this;
    nothrow(): this;
  }>;
}

export const aliceAuto = async ({
  $,
  directory,
}: {
  $: Shell;
  directory: string;
  [key: string]: any;
}) => {
  const serverUrl = process.env.ALICE_SERVER_URL;
  if (!serverUrl) return {};

  // Check if alice binary exists
  const binCheck = await $`test -x ${ALICE_BIN}`.nothrow();
  if (binCheck.exitCode !== 0) return {};

  return {
    "message.updated": async (input: any) => {
      if (!taskStarted && input?.message?.role === "user") {
        taskPrompt = (input.message.parts?.[0]?.text || "").slice(0, 80);
        taskStarted = true;
      }
    },

    "session.created": async () => {
      if (aliceReady) return;
      if (await hasAliceSession($)) {
        aliceReady = true;
        return;
      }
      aliceReady = await registerAlice($, serverUrl);
    },

    "session.idle": async () => {
      if (!aliceReady || !taskStarted) return;
      const summary = taskPrompt || "task";
      await $`${ALICE_BIN} --json publish \
        --type status_delta \
        --title ${summary} \
        --content ${summary} \
        --confidence 1.0`.quiet().nothrow();
      taskPrompt = "";
      taskStarted = false;
    },
  };
};
