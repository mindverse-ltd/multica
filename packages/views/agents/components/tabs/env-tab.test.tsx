// @vitest-environment jsdom

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enAgents from "../../../locales/en/agents.json";
import { EnvTab, formatEnvText, parseEnvText } from "./env-tab";

const apiMocks = vi.hoisted(() => ({
  getAgentEnv: vi.fn(),
  updateAgentEnv: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getAgentEnv: apiMocks.getAgentEnv,
    updateAgentEnv: apiMocks.updateAgentEnv,
  },
}));

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
    custom_args: [],
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

function renderEnvTab(agent: Agent, onSaved = vi.fn()) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <EnvTab agent={agent} onSaved={onSaved} />
    </I18nProvider>,
  );
  return { onSaved };
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

describe("formatEnvText", () => {
  it("formats one KEY=value pair per line", () => {
    expect(formatEnvText({ FOO: "bar", TOKEN: "a=b=c" })).toBe(
      "FOO=bar\nTOKEN=a=b=c",
    );
  });
});

describe("EnvTab bulk paste", () => {
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
    vi.clearAllMocks();
  });

  it("prefills the bulk textarea after reveal", async () => {
    apiMocks.getAgentEnv.mockResolvedValue({
      custom_env: { EXISTING: "old", TOKEN: "a=b=c" },
    });

    renderEnvTab(makeAgent({ custom_env_key_count: 2 }));

    fireEvent.click(screen.getByRole("button", { name: "Reveal & edit" }));

    await waitFor(() => {
      expect(screen.getByPlaceholderText(/ANTHROPIC_API_KEY/)).toHaveValue(
        "EXISTING=old\nTOKEN=a=b=c",
      );
    });
  });

  it("replaces the env with the parsed map and saves omitted keys as removed", async () => {
    apiMocks.getAgentEnv.mockResolvedValue({
      custom_env: { EXISTING: "old", REMOVED: "gone" },
    });
    apiMocks.updateAgentEnv.mockResolvedValue({
      custom_env: { EXISTING: "new", ADDED: "value" },
    });

    const { onSaved } = renderEnvTab(makeAgent({ custom_env_key_count: 1 }));

    fireEvent.click(screen.getByRole("button", { name: "Reveal & edit" }));

    fireEvent.change(await screen.findByPlaceholderText(/ANTHROPIC_API_KEY/), {
      target: { value: "EXISTING=new\nADDED=value" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Parse" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(apiMocks.updateAgentEnv).toHaveBeenCalledWith("agent-1", {
        custom_env: { EXISTING: "new", ADDED: "value" },
      });
      expect(onSaved).toHaveBeenCalledOnce();
    });
  });

  it("can parse an empty textarea as an empty replacement map", async () => {
    apiMocks.getAgentEnv.mockResolvedValue({
      custom_env: { EXISTING: "old" },
    });
    apiMocks.updateAgentEnv.mockResolvedValue({
      custom_env: {},
    });

    renderEnvTab(makeAgent({ custom_env_key_count: 1 }));

    fireEvent.click(screen.getByRole("button", { name: "Reveal & edit" }));

    fireEvent.change(await screen.findByPlaceholderText(/ANTHROPIC_API_KEY/), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Parse" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(apiMocks.updateAgentEnv).toHaveBeenCalledWith("agent-1", {
        custom_env: {},
      });
    });
  });
});
