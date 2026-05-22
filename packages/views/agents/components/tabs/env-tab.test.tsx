// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enAgents from "../../../locales/en/agents.json";
import { EnvTab, parseEnvText } from "./env-tab";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const TEST_RESOURCES = { en: { agents: enAgents } };

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    name: "Agent",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_env: {},
    custom_args: [],
    custom_env_redacted: false,
    visibility: "private",
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: "user-1",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
  };
}

function renderEnvTab(agent: Agent, onSave = vi.fn().mockResolvedValue(undefined)) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <EnvTab agent={agent} onSave={onSave} />
    </I18nProvider>,
  );
  return { onSave };
}

describe("parseEnvText", () => {
  it("parses one KEY=value pair per line and preserves equals in values", () => {
    expect(parseEnvText("FOO=bar\nTOKEN=a=b=c")).toEqual({
      entries: [
        { key: "FOO", value: "bar" },
        { key: "TOKEN", value: "a=b=c" },
      ],
      invalidLines: [],
      duplicateKeys: [],
    });
  });

  it("reports invalid lines and duplicate keys", () => {
    expect(parseEnvText("FOO=1\nmissing\nBAD KEY=2\nFOO=3")).toEqual({
      entries: [
        { key: "FOO", value: "1" },
        { key: "FOO", value: "3" },
      ],
      invalidLines: [2, 3],
      duplicateKeys: ["FOO"],
    });
  });
});

describe("EnvTab bulk paste", () => {
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
    vi.clearAllMocks();
  });

  it("updates existing keys, appends new keys, and saves the parsed map", () => {
    const { onSave } = renderEnvTab(
      makeAgent({ custom_env: { EXISTING: "old" } }),
    );

    fireEvent.change(screen.getByPlaceholderText(/ANTHROPIC_API_KEY/), {
      target: { value: "EXISTING=new\nADDED=value" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Parse" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(onSave).toHaveBeenCalledWith({
      custom_env: { EXISTING: "new", ADDED: "value" },
    });
  });
});
