import type { MessageBlock } from "../../types";

export function MessageContent({ blocks }: { blocks: MessageBlock[] }) {
  return (
    <>
      {blocks.map((block, index) => {
        if (block.type === "paragraph") {
          return (
            <p
              key={index}
              className="m-0 mb-1.5 text-[13px] last:mb-0"
              dangerouslySetInnerHTML={{ __html: block.html }}
            />
          );
        }

        return (
          <div
            key={index}
            className="mt-2 overflow-hidden rounded-lg border border-border bg-surface first:mt-0"
          >
            <div className="border-b border-border bg-surface-2 px-2.5 py-1.5 text-[10.5px] font-semibold tracking-wide text-text-muted uppercase">
              {block.heading}
            </div>
            <div className="px-2.5 py-2.5 text-[12.5px]">
              {block.kv && (
                <div className="grid grid-cols-2 gap-x-3 gap-y-1.5">
                  {block.kv.map((item) => (
                    <div key={item.label} className="text-xs">
                      <span className="block text-[10px] text-text-muted">
                        {item.label}
                      </span>
                      <b className="mono font-medium break-all">{item.value}</b>
                    </div>
                  ))}
                </div>
              )}
              {block.note && (
                <p className="mt-2 mb-0 font-mono text-[11.5px] whitespace-pre-wrap text-text-muted">
                  {block.note}
                </p>
              )}
            </div>
          </div>
        );
      })}
    </>
  );
}
