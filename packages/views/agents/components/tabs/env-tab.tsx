"use client";

import { useEffect, useState } from "react";
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
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "sonner";
import { useT } from "../../../i18n";

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

function entryWithKey(key: string, value: string): EnvEntry {
  return {
    id: nextEnvId++,
    key,
    value,
    visible: false,
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
  readOnly = false,
  onSave,
  onDirtyChange,
}: {
  agent: Agent;
  readOnly?: boolean;
  onSave: (updates: Partial<Agent>) => Promise<void>;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const { t } = useT("agents");
  const [envEntries, setEnvEntries] = useState<EnvEntry[]>(
    envMapToEntries(agent.custom_env ?? {}),
  );
  const [bulkText, setBulkText] = useState("");
  const [saving, setSaving] = useState(false);

  const currentEnvMap = entriesToEnvMap(envEntries);
  const originalEnvMap = agent.custom_env ?? {};
  const dirty =
    JSON.stringify(currentEnvMap) !== JSON.stringify(originalEnvMap);

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  const addEnvEntry = () => {
    setEnvEntries([
      ...envEntries,
      { id: nextEnvId++, key: "", value: "", visible: true },
    ]);
  };

  const removeEnvEntry = (index: number) => {
    setEnvEntries(envEntries.filter((_, i) => i !== index));
  };

  const updateEnvEntry = (
    index: number,
    field: "key" | "value",
    val: string,
  ) => {
    setEnvEntries(
      envEntries.map((entry, i) =>
        i === index ? { ...entry, [field]: val } : entry,
      ),
    );
  };

  const toggleEnvVisibility = (index: number) => {
    setEnvEntries(
      envEntries.map((entry, i) =>
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
    if (result.entries.length === 0) {
      return;
    }

    const nextEntries: EnvEntry[] = [...envEntries];
    for (const parsedEntry of result.entries) {
      const existingIndex = nextEntries.findIndex(
        (entry) => entry.key.trim() === parsedEntry.key,
      );
      if (existingIndex >= 0) {
        const existingEntry = nextEntries[existingIndex];
        if (!existingEntry) {
          continue;
        }
        nextEntries[existingIndex] = {
          id: existingEntry.id,
          key: parsedEntry.key,
          value: parsedEntry.value,
          visible: existingEntry.visible,
        };
      } else {
        nextEntries.push(entryWithKey(parsedEntry.key, parsedEntry.value));
      }
    }

    setEnvEntries(nextEntries);
    setBulkText("");
  };

  const handleSave = async () => {
    const keys = envEntries.filter((e) => e.key.trim()).map((e) => e.key.trim());
    const uniqueKeys = new Set(keys);
    if (uniqueKeys.size < keys.length) {
      toast.error(t(($) => $.tab_body.env.duplicate_keys_toast));
      return;
    }

    setSaving(true);
    try {
      await onSave({ custom_env: currentEnvMap });
      toast.success(t(($) => $.tab_body.env.saved_toast));
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

  if (readOnly) {
    return (
      <div className="space-y-4">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.tab_body.env.intro_readonly)}
        </p>
        {envEntries.length > 0 ? (
          <div className="space-y-2">
            {envEntries.map((entry) => (
              <div key={entry.id} className="flex items-center gap-2">
                <Input
                  value={entry.key}
                  readOnly
                  className="w-[40%] bg-muted font-mono text-xs"
                />
                <div className="relative flex-1">
                  <Input
                    type="password"
                    value="****"
                    readOnly
                    className="bg-muted pr-8 font-mono text-xs"
                  />
                  <Lock className="absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-xs italic text-muted-foreground">
            {t(($) => $.tab_body.env.empty_readonly)}
          </p>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.tab_body.env.intro_prefix)}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">
            {"ANTHROPIC_API_KEY"}
          </code>
          {t(($) => $.tab_body.env.intro_separator)}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">
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
            disabled={!bulkText.trim()}
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

      {envEntries.length > 0 && (
        <div className="space-y-2">
          {envEntries.map((entry, index) => (
            <div key={entry.id} className="flex items-center gap-2">
              <Input
                value={entry.key}
                onChange={(e) => updateEnvEntry(index, "key", e.target.value)}
                placeholder={t(($) => $.tab_body.env.key_placeholder)}
                className="w-[40%] font-mono text-xs"
              />
              <div className="relative flex-1">
                <Input
                  type={entry.visible ? "text" : "password"}
                  value={entry.value}
                  onChange={(e) =>
                    updateEnvEntry(index, "value", e.target.value)
                  }
                  placeholder={t(($) => $.tab_body.env.value_placeholder)}
                  className="pr-8 font-mono text-xs"
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
      )}

      <div className="flex items-center justify-end gap-3">
        {dirty && (
          <span className="text-xs text-muted-foreground">{t(($) => $.tab_body.common.unsaved_changes)}</span>
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
