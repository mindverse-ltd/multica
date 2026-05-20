import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import {
  CONNECT_REMOTE_COMMANDS,
  buildRuntimeConnectionBaseline,
  findNewlyConnectedRuntime,
} from "./connect-remote-dialog";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "runtime-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Claude (remote-host)",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "offline",
    device_info: "remote-host",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    timezone: "UTC",
    last_seen_at: null,
    created_at: "2026-05-20T00:00:00Z",
    updated_at: "2026-05-20T00:00:00Z",
    ...overrides,
  };
}

describe("connect remote commands", () => {
  it("keeps required spaces before long flags", () => {
    expect(CONNECT_REMOTE_COMMANDS.login).toContain("login --token");
    expect(CONNECT_REMOTE_COMMANDS.login).not.toContain("login--token");
    expect(CONNECT_REMOTE_COMMANDS.start).toContain("start --device-name");
    expect(CONNECT_REMOTE_COMMANDS.start).not.toContain("start--device-name");
  });
});

describe("runtime connection detection", () => {
  it("detects a newly registered online runtime", () => {
    const existing = makeRuntime({ id: "old-runtime", status: "offline" });
    const baseline = buildRuntimeConnectionBaseline([existing]);
    const created = makeRuntime({ id: "new-runtime", status: "online" });

    expect(findNewlyConnectedRuntime([existing, created], baseline)).toBe(
      created,
    );
  });

  it("detects an existing runtime reconnecting from offline to online", () => {
    const baseline = buildRuntimeConnectionBaseline([
      makeRuntime({ id: "runtime-1", status: "offline" }),
    ]);
    const reconnected = makeRuntime({ id: "runtime-1", status: "online" });

    expect(findNewlyConnectedRuntime([reconnected], baseline)).toBe(
      reconnected,
    );
  });

  it("ignores runtimes that were already online before waiting started", () => {
    const alreadyOnline = makeRuntime({ status: "online" });
    const baseline = buildRuntimeConnectionBaseline([alreadyOnline]);

    expect(findNewlyConnectedRuntime([alreadyOnline], baseline)).toBeNull();
  });
});
