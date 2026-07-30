"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Eye,
  EyeOff,
  FileText,
  Loader2,
  Lock,
  Plus,
  Save,
  Trash2,
} from "lucide-react";
import { api } from "@multica/core/api";
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "sonner";
import { useT } from "../../../i18n";

// Env values never reach this component until the user clicks
// "Reveal & edit" — the agent resource feed no longer carries
// custom_env at all after MUL-2600. Until then we display only the
// configured-key count from `agent.custom_env_key_count`, which is
// safe because it's not the values themselves.

let nextEnvId = 0;

interface EnvEntry {
  id: number;
  key: string;
  value: string;
  visible: boolean;
}

export interface EnvParseResult {
  entries: Array<{ key: string; value: string }>;
  invalidLines: number[];
  duplicateKeys: string[];
}

function envMapToEntries(env: Record<string, string>): EnvEntry[] {
  return Object.entries(env).map(([key, value]) => ({
    id: nextEnvId++,
    key,
    value,
    visible: false,
  }));
}

export function formatEnvText(env: Record<string, string>): string {
  return Object.entries(env)
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

function entriesToEnvMap(entries: EnvEntry[]): Record<string, string> {
  const map: Record<string, string> = {};
  for (const entry of entries) {
    const key = entry.key.trim();
    if (key) {
      map[key] = entry.value;
    }
  }
  return map;
}

function entryWithKey(key: string, value: string, visible = false): EnvEntry {
  return {
    id: nextEnvId++,
    key,
    value,
    visible,
  };
}

export function parseEnvText(text: string): EnvParseResult {
  const entries: Array<{ key: string; value: string }> = [];
  const invalidLines: number[] = [];
  const seenKeys = new Set<string>();
  const duplicateKeys = new Set<string>();

  text.split(/\r?\n/).forEach((rawLine, index) => {
    const line = rawLine.trim();
    if (!line) {
      return;
    }

    const separatorIndex = line.indexOf("=");
    if (separatorIndex <= 0) {
      invalidLines.push(index + 1);
      return;
    }

    const key = line.slice(0, separatorIndex).trim();
    if (!key || /\s/.test(key)) {
      invalidLines.push(index + 1);
      return;
    }

    if (seenKeys.has(key)) {
      duplicateKeys.add(key);
    }
    seenKeys.add(key);
    entries.push({ key, value: line.slice(separatorIndex + 1) });
  });

  return { entries, invalidLines, duplicateKeys: Array.from(duplicateKeys) };
}

export function EnvTab({
  agent,
  onDirtyChange,
  onSaved,
}: {
  agent: Agent;
  onDirtyChange?: (dirty: boolean) => void;
  // Notifier so the parent page can refresh its agent cache after a
  // successful PUT — the parent owns the `Agent` object the rest of
  // the page reads (name, has_custom_env, etc.). Optional so call
  // sites without invalidation logic stay simple.
  onSaved?: () => void;
}) {
  const { t } = useT("agents");

  // revealed === null means "haven't fetched yet"; revealed === [] is
  // a legitimate empty map after a successful reveal. We do NOT
  // pre-populate from `agent` here because the agent resource shape
  // no longer carries values — only the dedicated `/env` endpoint
  // does, and that endpoint writes an audit row per call so we never
  // fetch implicitly on mount.
  const [revealed, setRevealed] = useState<EnvEntry[] | null>(null);
  const [originalMap, setOriginalMap] = useState<Record<string, string>>({});
  const [revealing, setRevealing] = useState(false);
  const [bulkText, setBulkText] = useState("");
  const [saving, setSaving] = useState(false);

  const keyCount = agent.custom_env_key_count ?? 0;

  const currentEnvMap = revealed ? entriesToEnvMap(revealed) : originalMap;
  const dirty =
    revealed !== null &&
    JSON.stringify(currentEnvMap) !== JSON.stringify(originalMap);

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  const handleReveal = useCallback(async () => {
    setRevealing(true);
    try {
      const resp = await api.getAgentEnv(agent.id);
      const env = resp.custom_env ?? {};
      setOriginalMap(env);
      setRevealed(envMapToEntries(env));
      setBulkText(formatEnvText(env));
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.tab_body.env.reveal_failed_toast),
      );
    } finally {
      setRevealing(false);
    }
  }, [agent.id, t]);

  const addEnvEntry = () => {
    setRevealed((prev) => [
      ...(prev ?? []),
      { id: nextEnvId++, key: "", value: "", visible: true },
    ]);
  };

  const removeEnvEntry = (index: number) => {
    setRevealed((prev) => (prev ?? []).filter((_, i) => i !== index));
  };

  const updateEnvEntry = (
    index: number,
    field: "key" | "value",
    val: string,
  ) => {
    setRevealed((prev) =>
      (prev ?? []).map((entry, i) =>
        i === index ? { ...entry, [field]: val } : entry,
      ),
    );
  };

  const toggleEnvVisibility = (index: number) => {
    setRevealed((prev) =>
      (prev ?? []).map((entry, i) =>
        i === index ? { ...entry, visible: !entry.visible } : entry,
      ),
    );
  };

  const handleBulkApply = () => {
    const result = parseEnvText(bulkText);
    if (result.invalidLines.length > 0) {
      toast.error(
        t(($) => $.tab_body.env.bulk_invalid_toast, {
          lines: result.invalidLines.join(", "),
        }),
      );
      return;
    }
    if (result.duplicateKeys.length > 0) {
      toast.error(
        t(($) => $.tab_body.env.bulk_duplicate_toast, {
          keys: result.duplicateKeys.join(", "),
        }),
      );
      return;
    }

    setRevealed(
      result.entries.map((parsedEntry) =>
        entryWithKey(parsedEntry.key, parsedEntry.value, true),
      ),
    );
  };

  const handleSave = async () => {
    if (revealed === null) return;
    const keys = revealed.filter((e) => e.key.trim()).map((e) => e.key.trim());
    const uniqueKeys = new Set(keys);
    if (uniqueKeys.size < keys.length) {
      toast.error(t(($) => $.tab_body.env.duplicate_keys_toast));
      return;
    }

    setSaving(true);
    try {
      const resp = await api.updateAgentEnv(agent.id, {
        custom_env: currentEnvMap,
      });
      const env = resp.custom_env ?? {};
      setOriginalMap(env);
      setRevealed(envMapToEntries(env));
      setBulkText(formatEnvText(env));
      toast.success(t(($) => $.tab_body.env.saved_toast));
      onSaved?.();
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.tab_body.env.save_failed_toast),
      );
    } finally {
      setSaving(false);
    }
  };

  // Pre-reveal state: show count + Reveal button. We never auto-fetch
  // on mount so a member just navigating between tabs doesn't trigger
  // an audit-log entry; the reveal must be intentional.
  if (revealed === null) {
    return (
      <div className="space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div className="space-y-1">
            <p className="flex items-center gap-2 text-body font-medium">
              <Lock className="h-3.5 w-3.5 text-muted-foreground" />
              {keyCount > 0
                ? t(($) => $.tab_body.env.not_revealed_title, {
                    count: keyCount,
                  })
                : t(($) => $.tab_body.env.not_revealed_empty)}
            </p>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.tab_body.env.not_revealed_hint)}
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={revealing}
            onClick={handleReveal}
            className="shrink-0"
          >
            {revealing ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Eye className="h-3.5 w-3.5" />
            )}
            {revealing
              ? t(($) => $.tab_body.env.revealing)
              : t(($) => $.tab_body.env.reveal_action)}
          </Button>
        </div>
      </div>
    );
  }

  // Editable state — only entered after a successful reveal.
  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <p className="text-caption text-muted-foreground">
          {t(($) => $.tab_body.env.intro_prefix)}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-micro">
            {"ANTHROPIC_API_KEY"}
          </code>
          {t(($) => $.tab_body.env.intro_separator)}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-micro">
            {"ANTHROPIC_BASE_URL"}
          </code>
          {t(($) => $.tab_body.env.intro_suffix)}
        </p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addEnvEntry}
          className="shrink-0"
        >
          <Plus className="h-3 w-3" />
          {t(($) => $.tab_body.common.add)}
        </Button>
      </div>

      <div className="space-y-2 rounded-md border border-dashed bg-muted/20 p-3">
        <div className="flex items-start justify-between gap-3">
          <div className="space-y-1">
            <div className="text-xs font-medium">
              {t(($) => $.tab_body.env.bulk_title)}
            </div>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.tab_body.env.bulk_hint)}
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleBulkApply}
            className="shrink-0"
          >
            <FileText className="h-3.5 w-3.5" />
            {t(($) => $.tab_body.env.bulk_apply)}
          </Button>
        </div>
        <Textarea
          value={bulkText}
          onChange={(e) => setBulkText(e.target.value)}
          placeholder={t(($) => $.tab_body.env.bulk_placeholder)}
          className="min-h-24 font-mono text-xs"
        />
      </div>

      {revealed.length > 0 ? (
        <div className="space-y-2">
          {revealed.map((entry, index) => (
            <div key={entry.id} className="flex items-center gap-2">
              <Input
                value={entry.key}
                onChange={(e) => updateEnvEntry(index, "key", e.target.value)}
                placeholder={t(($) => $.tab_body.env.key_placeholder)}
                className="w-[40%] font-mono text-caption"
              />
              <div className="relative flex-1">
                <Input
                  type={entry.visible ? "text" : "password"}
                  value={entry.value}
                  onChange={(e) =>
                    updateEnvEntry(index, "value", e.target.value)
                  }
                  placeholder={t(($) => $.tab_body.env.value_placeholder)}
                  className="pr-8 font-mono text-caption"
                />
                <button
                  type="button"
                  onClick={() => toggleEnvVisibility(index)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  aria-label={entry.visible ? t(($) => $.tab_body.env.hide_value_aria) : t(($) => $.tab_body.env.show_value_aria)}
                >
                  {entry.visible ? (
                    <EyeOff className="h-3.5 w-3.5" />
                  ) : (
                    <Eye className="h-3.5 w-3.5" />
                  )}
                </button>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => removeEnvEntry(index)}
                className="text-muted-foreground hover:text-destructive"
                aria-label={t(($) => $.tab_body.env.remove_aria)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
        </div>
      ) : (
        <p className="text-caption italic text-muted-foreground">
          {t(($) => $.tab_body.env.empty_editable)}
        </p>
      )}

      <div className="flex items-center justify-end gap-3">
        {dirty && (
          <span className="text-caption text-muted-foreground">{t(($) => $.tab_body.common.unsaved_changes)}</span>
        )}
        <Button onClick={handleSave} disabled={!dirty || saving} size="sm">
          {saving ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Save className="h-3.5 w-3.5" />
          )}
          {t(($) => $.tab_body.common.save)}
        </Button>
      </div>
    </div>
  );
}
