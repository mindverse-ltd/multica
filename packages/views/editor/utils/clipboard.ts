import { copyTextToClipboard } from "@multica/ui/lib/clipboard";

/**
 * Copy markdown content to the clipboard.
 */
export async function copyMarkdown(markdown: string): Promise<void> {
  await copyTextToClipboard(markdown);
}
