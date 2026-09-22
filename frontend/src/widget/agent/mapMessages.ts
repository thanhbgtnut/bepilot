import type { AssistantMessage, InputContent, Message, ToolCall, UserMessage } from "@ag-ui/client";
import type { ChatMessage, MessageBlock } from "../types";
import { escapeHtml } from "../utils";

// User messages can carry multimodal parts (images, audio, ...); this widget
// only renders the text portions — attachments are out of scope for now.
function userContentToText(content: UserMessage["content"]): string {
  if (typeof content === "string") return content;
  return content
    .filter((part: InputContent) => part.type === "text")
    .map((part) => (part as { type: "text"; text: string }).text)
    .join("\n");
}

// Minimal inline formatting the appraisal agent tends to emit: **bold** and
// `code`. Everything else stays literal text. Run AFTER escapeHtml so the
// injected tags are the only markup.
function inlineFormat(escaped: string): string {
  return escaped
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/`([^`]+)`/g, '<code style="font-family:ui-monospace,monospace">$1</code>');
}

function textBlock(content: string | undefined): MessageBlock[] {
  const text = content?.trim();
  if (!text) return []; // skip empty/whitespace-only turns (reasoning-model artifacts)
  return [{ type: "paragraph", html: inlineFormat(escapeHtml(text)).replace(/\n/g, "<br />") }];
}

/**
 * Tool call rendered as a card: flat JSON arguments become key/value rows,
 * anything else (still-streaming or nested JSON) falls back to a raw note.
 */
function toolCallBlock(toolCall: ToolCall): MessageBlock {
  try {
    const args: unknown = JSON.parse(toolCall.function.arguments || "{}");
    if (args && typeof args === "object" && !Array.isArray(args)) {
      const kv = Object.entries(args as Record<string, unknown>).map(([label, value]) => ({
        label,
        value: typeof value === "string" ? value : JSON.stringify(value),
      }));
      return { type: "card", heading: `⚙ ${toolCall.function.name}`, kv };
    }
  } catch {
    // Arguments are still streaming in or aren't flat JSON — show raw text.
  }
  return { type: "card", heading: `⚙ ${toolCall.function.name}`, note: toolCall.function.arguments };
}

// The appraisal backend returns structured tool results (summary / confidence /
// metrics / reviewer_notes / source_refs). Render the useful bits instead of a
// raw JSON dump; fall back to truncated text for anything else.
function toolResultBlock(raw: string | undefined): MessageBlock {
  const text = raw ?? "";
  try {
    const r = JSON.parse(text) as Record<string, unknown>;
    if (r && typeof r === "object" && ("summary" in r || "task_label" in r || "decision" in r)) {
      const kv: { label: string; value: string }[] = [];
      const metrics = r.metrics as Record<string, unknown> | undefined;
      for (const [k, v] of Object.entries(metrics ?? {})) {
        if (typeof v === "number" || typeof v === "string") kv.push({ label: k, value: String(v) });
      }
      if (typeof r.confidence === "number") kv.push({ label: "độ tin cậy", value: String(r.confidence) });
      if (typeof r.status === "string") kv.push({ label: "trạng thái", value: r.status });
      const notes = Array.isArray(r.reviewer_notes) ? (r.reviewer_notes as string[]) : [];
      const noteParts: string[] = [];
      if (typeof r.summary === "string") noteParts.push(r.summary);
      if (notes.length) noteParts.push("Cần xác nhận:\n" + notes.map((n) => `• ${n}`).join("\n"));
      return {
        type: "card",
        heading: typeof r.task_label === "string" ? `↳ ${r.task_label}` : "↳ Kết quả công cụ",
        kv: kv.length ? kv : undefined,
        note: noteParts.join("\n\n") || undefined,
      };
    }
  } catch {
    // not JSON — fall through
  }
  return {
    type: "card",
    heading: "↳ Kết quả công cụ",
    note: text.length > 600 ? `${text.slice(0, 600)}…` : text,
  };
}

export function mapAgMessagesToChat(messages: readonly Message[]): ChatMessage[] {
  const chatMessages: ChatMessage[] = [];

  for (const message of messages) {
    if (message.role === "user") {
      const text = userContentToText(message.content);
      chatMessages.push({ id: message.id, side: "me", blocks: textBlock(text) });
      continue;
    }

    if (message.role === "assistant") {
      const assistant = message as AssistantMessage;
      const blocks: MessageBlock[] = [
        ...textBlock(assistant.content),
        ...(assistant.toolCalls ?? []).map(toolCallBlock),
      ];
      if (blocks.length > 0) {
        chatMessages.push({ id: assistant.id, side: "ai", blocks });
      }
      continue;
    }

    if (message.role === "tool") {
      chatMessages.push({
        id: message.id,
        side: "ai",
        blocks: [toolResultBlock(message.content)],
      });
    }

    // system / developer messages are backend-internal prompts, not shown.
  }

  return chatMessages;
}
